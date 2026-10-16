package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/vigil-tracker/agent/internal/api"
	"github.com/vigil-tracker/agent/internal/app"
	"github.com/vigil-tracker/agent/internal/config"
	"github.com/vigil-tracker/agent/internal/source"
	"github.com/vigil-tracker/agent/internal/source/bbportlog"
	"github.com/vigil-tracker/agent/internal/syncer"
)

// playLogPath is where "play" saves the game's output: the agent's own folder, never the port's.
func playLogPath(store *config.Store) string { return app.PlayLogPath(store) }

// configuredSources opens every configured source except one following skipLog.
func configuredSources(store *config.Store, logger *log.Logger, skipLog string) ([]source.Source, error) {
	var keep []config.SourceConfig
	for _, c := range store.Sources() {
		if c.Type == source.BBPortLog && app.SameFile(c.Path, skipLog) {
			continue
		}
		keep = append(keep, c)
	}
	return app.OpenSources(keep, logger)
}

// play starts the official bbport, shows its output and uploads the trophies it prints. The game
// writes to a file (not a pipe) and runs detached, so closing the agent never kills or blocks
// it; "vigil-agent run" picks up where play stopped.
//
//	vigil-agent play bbport [--game-dir DIR] [--exit-with-game] -- <program> [args...]
func play(ctx context.Context, store *config.Store, args []string, logger *log.Logger) error {
	if len(args) == 0 || args[0] != "bbport" {
		return errors.New("usage: vigil-agent play bbport [--game-dir DIR] [--exit-with-game] -- <program> [args...]")
	}
	fs := flag.NewFlagSet("play bbport", flag.ContinueOnError)
	gameDir := fs.String("game-dir", "", "game folder (sce_sys/param.sfo, npbind.dat); default: the bbport-log source's")
	exitWithGame := fs.Bool("exit-with-game", false, "stop the agent when the game exits (default: keep watching sources)")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	command := fs.Args()
	if len(command) == 0 {
		return errors.New("give the command that starts bbport after --, e.g.: vigil-agent play bbport -- bash /path/to/bbport/run.sh")
	}
	if *gameDir == "" {
		*gameDir = app.ConfiguredGameDir(store)
	}

	token, err := store.Token()
	if err != nil {
		return err
	}
	if token == "" {
		return errors.New("not linked yet; run: vigil-agent link")
	}
	client, err := api.New(store.Server(), token)
	if err != nil {
		return err
	}

	logPath := playLogPath(store)
	if err := os.MkdirAll(filepath.Dir(logPath), 0o700); err != nil {
		return err
	}
	out, err := os.OpenFile(logPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	// The program and its arguments go to the OS as they are: no shell interprets them.
	cmd := exec.Command(command[0], command[1:]...)
	cmd.Stdout, cmd.Stderr = out, out
	detach(cmd)
	if err := cmd.Start(); err != nil {
		out.Close()
		return fmt.Errorf("cannot start %q: %w", command[0], err)
	}
	out.Close() // the game keeps its own handle
	logger.Printf("started %s (pid %d); its output is saved to %s", command[0], cmd.Process.Pid, logPath)

	game := app.Identify(*gameDir, logger)
	live, err := bbportlog.New(logPath, game)
	if err != nil {
		return err
	}
	live.Echo = func(line string) { fmt.Println(line) }
	srcs, err := configuredSources(store, logger, logPath)
	if err != nil {
		return err
	}
	s, err := syncer.New(client, store, append([]source.Source{live}, srcs...), logger)
	if err != nil {
		return err
	}
	s.Interval = time.Second
	s.Proposer = app.Proposer(client, store, srcs, logger)

	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()
	runCtx, stopSync := context.WithCancel(ctx)
	defer stopSync()
	synced := make(chan error, 1)
	go func() { synced <- s.Run(runCtx) }()

	select {
	case <-ctx.Done():
		stopSync()
		<-synced
		logger.Printf("agent stopped; the game keeps running. Its trophies are still saved to %s, "+
			"and \"vigil-agent run\" uploads them later.", logPath)
		return nil
	case err := <-synced:
		if errors.Is(err, api.ErrUnauthorized) {
			return errors.New("the server rejected the token (revoked?); run: vigil-agent link (the game keeps running)")
		}
		return err
	case err := <-exited:
		code := 0
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			code = exitErr.ExitCode()
		}
		logger.Printf("the game exited (code %d)", code)
		if !*exitWithGame {
			logger.Printf("back to watching the configured sources (Ctrl+C to stop)")
			err := <-synced
			if errors.Is(err, api.ErrUnauthorized) {
				return errors.New("the server rejected the token (revoked?); run: vigil-agent link")
			}
			return err
		}
		// Last read of the log, then send what is queued before leaving.
		stopSync()
		<-synced
		flushCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if err := s.Scan(flushCtx); err != nil {
			logger.Printf("error: %v", err)
		}
		if err := s.Flush(flushCtx); err != nil {
			return err
		}
		logger.Printf("stopped; %d event(s) pending", s.Pending())
		return nil
	}
}
