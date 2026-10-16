// Command vigil-agent links this PC to a Vigil Trophies account and uploads the trophies that
// PC ports and emulators record locally (bbport, RPCS3, shadPS4). It only reads those files.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/vigil-tracker/agent/internal/api"
	"github.com/vigil-tracker/agent/internal/app"
	"github.com/vigil-tracker/agent/internal/config"
	"github.com/vigil-tracker/agent/internal/outbox"
	"github.com/vigil-tracker/agent/internal/source"
	"github.com/vigil-tracker/agent/internal/source/bbportlog"
	"github.com/vigil-tracker/agent/internal/source/detect"
	"github.com/vigil-tracker/agent/internal/source/shadps4"
	"github.com/vigil-tracker/agent/internal/syncer"
)

const usage = `vigil-agent %s — Vigil Trophies agent for PC ports and emulators

Usage:
  vigil-agent link    [--server URL] [--name "PC name"]  link this PC to your account
  vigil-agent run     [--user-dir PATH]                  watch every configured source and upload unlocks
  vigil-agent play    bbport [--game-dir DIR] -- CMD...  start the official bbport and upload its trophies
  vigil-agent status                                     show server, link, sources and queue
  vigil-agent unlink                                     forget the local token
  vigil-agent version                                    print the version
  vigil-agent check-update                               look for a newer version (opens nothing, downloads nothing)
  vigil-agent config  catalog-proposals on|off           send catalogs of unknown games (default on)
  vigil-agent config  server https://host               use another Vigil server (link again afterwards)
  vigil-agent notify-test [--style vigil|windows] [--grade G]  show example trophy notifications (Windows)

  vigil-agent sources detect [--in DIR]...               suggest source folders (read only, sends nothing)
  vigil-agent sources add <type> <path> [--game-dir DIR] [--update-dir DIR]
                                                         add a source (type: bbport-log, gow3-log, bbport, rpcs3, shadps4)
  vigil-agent sources remove <type> <path>               remove a source
  vigil-agent sources list                               list the configured sources

Source folders:
  bbport-log  the official bbport's user folder (the launcher writes last_run.log there);
              --game-dir is the game folder (sce_sys) that says which trophy set it is
  gow3-log    the God of War III port's saves folder (gow3_pc; its launcher writes last_run.log there);
              --game-dir is the game folder (CUSA01623...) and --update-dir the update folder; without
              them the port launcher's own settings are used. Never reported as Bloodborne.
  bbport      the port's "user" folder with the Vigil patch (trophies.jsonl); --user-dir is a shortcut
  rpcs3       the RPCS3 folder (holds dev_hdd0)
  shadps4     the shadPS4 "user" folder (holds home/ and trophy/)

The agent only reads these folders: it never writes, moves or deletes anything in them.
`

func main() {
	logger := log.New(os.Stderr, "", log.LstdFlags)
	if len(os.Args) < 2 {
		fmt.Fprintf(os.Stderr, usage, api.Version)
		os.Exit(2)
	}
	store, err := config.Open()
	if err != nil {
		logger.Fatalf("cannot open config directory: %v", err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	cmd, args := os.Args[1], os.Args[2:]
	switch cmd {
	case "link":
		err = link(ctx, store, args, os.Stdout)
	case "run":
		err = run(ctx, store, args, logger)
	case "status":
		err = status(store, os.Stdout)
	case "unlink":
		err = unlink(store, os.Stdout)
	case "sources":
		err = sources(ctx, store, args, os.Stdout)
	case "play":
		err = play(ctx, store, args, logger)
	case "config":
		err = configure(store, args, os.Stdout)
	case "notify-test":
		err = notifyTest(store, args, os.Stdout)
	case "version", "--version":
		fmt.Println(api.Version)
	case "check-update":
		err = checkUpdate(ctx, store, os.Stdout)
	case "help", "-h", "--help":
		fmt.Printf(usage, api.Version)
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n"+usage, cmd, api.Version)
		os.Exit(2)
	}
	if err != nil {
		logger.Printf("error: %v", err)
		stop()
		os.Exit(1)
	}
}

func link(ctx context.Context, store *config.Store, args []string, out io.Writer) error {
	fs := flag.NewFlagSet("link", flag.ContinueOnError)
	server := fs.String("server", store.Server(), "Vigil Trophies server URL")
	host, _ := os.Hostname()
	name := fs.String("name", host, "name shown in your device list")
	if err := fs.Parse(args); err != nil {
		return err
	}
	client, err := api.New(*server, "")
	if err != nil {
		return err
	}
	auth, err := client.Authorize(ctx)
	if err != nil {
		var retry *api.RetryableError
		if errors.As(err, &retry) {
			return fmt.Errorf("cannot reach %s (%w); nothing was linked. The server may not be online yet; "+
				"to use another one: vigil-agent config server https://host", client.BaseURL, err)
		}
		return fmt.Errorf("cannot start linking: %w", err)
	}

	link := auth.VerificationURIComplete
	if link == "" {
		link = auth.VerificationURI
	}
	if *name != "" {
		if u, err := url.Parse(link); err == nil {
			q := u.Query()
			q.Set("name", *name)
			u.RawQuery = q.Encode()
			link = u.String()
		}
	}
	fmt.Fprintf(out, "\nOpen this page, log in and confirm the code:\n\n    %s\n\n    code: %s\n\n", link, auth.UserCode)
	fmt.Fprintf(out, "Waiting for approval (expires in %d minutes, Ctrl+C to cancel)...\n", auth.ExpiresIn/60)

	token, err := app.WaitForToken(ctx, client, auth)
	if err != nil {
		return err
	}
	if err := store.SetServer(client.BaseURL); err != nil {
		return err
	}
	if err := store.SetToken(token); err != nil {
		return fmt.Errorf("cannot save token: %w", err)
	}
	fmt.Fprintf(out, "Linked. Token saved in %s\n", store.Dir)
	if store.CatalogProposalsEnabled() {
		fmt.Fprintln(out, "\nNote: when an emulator has the trophy list of a game the site does not know yet (names,"+
			"\ndescriptions and icons RPCS3/shadPS4 keep in plain files), the agent sends it so the game can be"+
			"\nadded to the catalog. To turn this off: vigil-agent config catalog-proposals off")
	}
	return nil
}

func run(ctx context.Context, store *config.Store, args []string, logger *log.Logger) error {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	userDir := fs.String("user-dir", "", "bbport user folder (shortcut for a bbport source, not saved)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	configs := store.Sources()
	if *userDir != "" {
		if info, err := os.Stat(*userDir); err != nil || !info.IsDir() {
			return fmt.Errorf("--user-dir %q is not a folder", *userDir)
		}
		configs = append(configs, config.SourceConfig{Type: source.BBPort, Path: *userDir})
	}
	if len(configs) == 0 {
		return errors.New("no sources configured; run: vigil-agent sources detect")
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
	srcs, err := app.OpenSources(configs, logger)
	if err != nil {
		return err
	}
	// Output saved by "play" (the game may still be running after the agent was closed).
	if logPath := app.PlayLogPath(store); app.FileExists(logPath) {
		src, err := bbportlog.New(logPath, app.Identify(app.ConfiguredGameDir(store), nil))
		if err != nil {
			return err
		}
		logger.Printf("watching %s: %s", source.BBPortLog, logPath)
		srcs = append(srcs, src)
	}
	s, err := syncer.New(client, store, srcs, logger)
	if err != nil {
		return err
	}
	s.Proposer = app.Proposer(client, store, srcs, logger)
	logger.Printf("vigil-agent %s, server %s, %d event(s) pending", api.Version, client.BaseURL, s.Pending())
	err = s.Run(ctx)
	if errors.Is(err, api.ErrUnauthorized) {
		return errors.New("the server rejected the token (revoked?); run: vigil-agent link")
	}
	if err == nil {
		logger.Printf("stopped; %d event(s) pending", s.Pending())
	}
	return err
}

// configure changes agent settings:
//
//	vigil-agent config catalog-proposals on|off
//	vigil-agent config server https://host
//
// The server is saved in config.json, shared with the tray app, so a build never has to be
// recompiled to talk to another server. A token belongs to the server that issued it: changing the
// server forgets the local token (it is never sent to another host) and the PC is linked again.
func configure(store *config.Store, args []string, out io.Writer) error {
	const usageText = "usage: vigil-agent config catalog-proposals on|off\n       vigil-agent config server https://host"
	if len(args) != 2 {
		return errors.New(usageText)
	}
	switch args[0] {
	case "catalog-proposals":
		if args[1] != "on" && args[1] != "off" {
			return errors.New(usageText)
		}
		if err := store.SetCatalogProposals(args[1] == "on"); err != nil {
			return err
		}
		fmt.Fprintf(out, "Catalog proposals: %s\n", args[1])
		return nil
	case "server":
		server, err := api.NormalizeServerURL(args[1])
		if err != nil {
			return err
		}
		if server == store.Server() {
			fmt.Fprintf(out, "Server: %s (unchanged)\n", server)
			return nil
		}
		token, err := store.Token()
		if err != nil {
			return err
		}
		if token != "" {
			if err := store.DeleteToken(); err != nil {
				return err
			}
		}
		if err := store.SetServer(server); err != nil {
			return err
		}
		fmt.Fprintf(out, "Server: %s\n", server)
		if token != "" {
			fmt.Fprintln(out, "The link to the previous server was forgotten here; link this PC again (vigil-agent link).")
		}
		fmt.Fprintln(out, "If the Vigil app is open, close it from its menu and open it again. Queued trophies are kept.")
		return nil
	}
	return errors.New(usageText)
}

func status(store *config.Store, out io.Writer) error {
	token, err := store.Token()
	if err != nil {
		return err
	}
	ob, err := outbox.Open(store.OutboxPath())
	if err != nil {
		return err
	}
	linked := "no (run: vigil-agent link)"
	if token != "" {
		linked = "yes"
	}
	fmt.Fprintf(out, "config:   %s\nserver:   %s\nlinked:   %s\n", store.Dir, store.Server(), linked)
	if len(store.Sources()) == 0 {
		fmt.Fprintf(out, "sources:  none (run: vigil-agent sources detect)\n")
	}
	for i, c := range store.Sources() {
		label := "sources: "
		if i > 0 {
			label = "         "
		}
		fmt.Fprintf(out, "%s %-10s %s%s\n", label, c.Type, c.Path, missing(c.Path))
	}
	fmt.Fprintf(out, "known:    %d unlock(s) already queued or sent\npending:  %d event(s)\n", len(store.Known()), ob.Len())
	onOff := "on"
	if !store.CatalogProposalsEnabled() {
		onOff = "off (vigil-agent config catalog-proposals on)"
	}
	fmt.Fprintf(out, "catalogs: proposals %s; %d trophy set(s) handled\n", onOff, len(store.HandledCatalogs()))
	return nil
}

func missing(path string) string {
	if info, err := os.Stat(path); err != nil || !info.IsDir() {
		return "  (folder not found)"
	}
	return ""
}

func unlink(store *config.Store, out io.Writer) error {
	if err := store.DeleteToken(); err != nil {
		return err
	}
	fmt.Fprintln(out, "Local token removed. To revoke it on the server too, use the device list on the website.")
	return nil
}

func sources(ctx context.Context, store *config.Store, args []string, out io.Writer) error {
	if len(args) == 0 {
		return errors.New("usage: vigil-agent sources detect|add|remove|list")
	}
	switch args[0] {
	case "list":
		if len(store.Sources()) == 0 {
			fmt.Fprintln(out, "No sources configured. Try: vigil-agent sources detect")
		}
		for _, c := range store.Sources() {
			fmt.Fprintf(out, "%-10s %s%s\n", c.Type, c.Path, missing(c.Path))
		}
		return nil
	case "add", "remove":
		if len(args) < 3 {
			return fmt.Errorf("usage: vigil-agent sources %s <type> <path> [--game-dir DIR] [--update-dir DIR]", args[0])
		}
		fs := flag.NewFlagSet("sources "+args[0], flag.ContinueOnError)
		gameDir := fs.String("game-dir", "", "bbport-log, gow3-log: the game folder (holds sce_sys)")
		updateDir := fs.String("update-dir", "", "gow3-log: the update folder the port's launcher uses")
		if err := fs.Parse(args[3:]); err != nil {
			return err
		}
		if fs.NArg() > 0 {
			return fmt.Errorf("unexpected argument %q", fs.Arg(0))
		}
		typ := strings.ToLower(args[1])
		if !slices.Contains(source.Types, typ) {
			return fmt.Errorf("unknown source type %q (use %s)", args[1], strings.Join(source.Types, ", "))
		}
		path, err := filepath.Abs(args[2])
		if err != nil {
			return err
		}
		c := config.SourceConfig{Type: typ, Path: path}
		if args[0] == "remove" {
			// Removing the folder's last source is a deliberate "stop watching": the tray app will not
			// offer the folder again until it is added back (sources add) or watched again.
			others := 0
			for _, e := range store.Sources() {
				if e.Type != c.Type && config.SameFolder(e.Path, c.Path) {
					others++
				}
			}
			if others > 0 {
				n, err := store.RemoveSource(c)
				if err == nil {
					fmt.Fprintf(out, "Removed %d source(s).\n", n)
				}
				return err
			}
			n, err := store.StopWatching(c.Path, time.Now())
			if err == nil {
				fmt.Fprintf(out, "Removed %d source(s). The folder will not be suggested again; add it back with: vigil-agent sources add %s %q\n",
					n, c.Type, c.Path)
			}
			return err
		}
		if info, err := os.Stat(path); err != nil || !info.IsDir() {
			return fmt.Errorf("%q is not a folder", path)
		}
		if typ == source.ShadPS4 {
			c.Path = shadps4.UserDir(path)
		}
		if *updateDir != "" {
			if typ != source.GOW3Log {
				return errors.New("--update-dir is only for gow3-log")
			}
			abs, err := filepath.Abs(*updateDir)
			if err != nil {
				return err
			}
			c.UpdateDir = abs
		}
		if *gameDir != "" && typ == source.GOW3Log {
			abs, err := filepath.Abs(*gameDir)
			if err != nil {
				return err
			}
			c.GameDir = abs
		} else if *gameDir != "" {
			if typ != source.BBPortLog {
				return errors.New("--game-dir is only for bbport-log and gow3-log")
			}
			abs, err := filepath.Abs(*gameDir)
			if err != nil {
				return err
			}
			if info, err := os.Stat(abs); err != nil || !info.IsDir() {
				return fmt.Errorf("%q is not a folder", abs)
			}
			game, err := bbportlog.Identify(abs)
			if err != nil {
				return err
			}
			fmt.Fprintf(out, "Game folder: %s (trophy set %q, title %q)\n", abs, game.TrophySetID, game.TitleID)
			c.GameDir = abs
		}
		if typ == source.GOW3Log {
			// Says now whether trophies will be reported, and for which set; never Bloodborne.
			if game, err := app.IdentifyGOW3(c); err != nil {
				fmt.Fprintf(out, "Warning: %v. Nothing is sent from this folder until the game folder is identified.\n", err)
			} else {
				fmt.Fprintf(out, "God of War III: trophy set %s, title %s\n", game.TrophySetID, game.TitleID)
			}
		}
		added, err := store.AddSource(c)
		if err != nil {
			return err
		}
		if added {
			fmt.Fprintf(out, "Added %s source %s\n", c.Type, c.Path)
		} else {
			fmt.Fprintf(out, "Already configured: %s %s\n", c.Type, c.Path)
		}
		return nil
	case "detect":
		return detectSources(ctx, store, args[1:], out)
	}
	return fmt.Errorf("unknown sources command %q", args[0])
}

type dirList []string

func (d *dirList) String() string     { return strings.Join(*d, ",") }
func (d *dirList) Set(v string) error { *d = append(*d, v); return nil }

// detectSources lists likely source folders and what each one holds. Read only: nothing is
// saved and nothing is sent to the server.
func detectSources(ctx context.Context, store *config.Store, args []string, out io.Writer) error {
	fs := flag.NewFlagSet("sources detect", flag.ContinueOnError)
	var extra dirList
	fs.Var(&extra, "in", "also look in this folder and its direct subfolders (repeatable)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	found := detect.Find(detect.FromOS(extra))
	if len(found) == 0 {
		fmt.Fprintln(out, "No sources found in the usual places. Add one with: vigil-agent sources add <type> <path>")
		return nil
	}
	configured := map[string]bool{}
	for _, c := range store.Sources() {
		configured[app.SourceKey(c)] = true
	}
	fmt.Fprintf(out, "Found %d possible source(s). Nothing was changed or sent.\n", len(found))
	for _, c := range found {
		cfg := config.SourceConfig{Type: c.Type, Path: c.Path, GameDir: c.GameDir, UpdateDir: c.UpdateDir}
		fmt.Fprintf(out, "\n  %-10s %s\n", c.Type, c.Path)
		src, err := app.OpenSource(cfg, nil)
		if err != nil {
			fmt.Fprintf(out, "             cannot open: %v\n", err)
			continue
		}
		scanCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		unlocks, err := src.Scan(scanCtx)
		cancel()
		lines := app.CountPerGame(unlocks)
		if len(lines) == 0 {
			lines = []string{"no unlocked trophies found yet"}
		}
		for _, line := range lines {
			fmt.Fprintf(out, "             %s\n", line)
		}
		if err != nil {
			fmt.Fprintf(out, "             some files could not be read: %v\n", err)
		}
		switch {
		case configured[app.SourceKey(cfg)]:
			fmt.Fprintf(out, "             (already configured)\n")
		case store.IsStopped(c.Path):
			fmt.Fprintf(out, "             (you stopped watching this folder; add it back with: vigil-agent sources add %s %q)\n",
				c.Type, c.Path)
		default:
			gameFlag := ""
			if c.GameDir != "" {
				gameFlag = fmt.Sprintf(" --game-dir \"%s\"", c.GameDir)
			}
			if c.UpdateDir != "" {
				gameFlag += fmt.Sprintf(" --update-dir \"%s\"", c.UpdateDir)
			}
			fmt.Fprintf(out, "             add with: vigil-agent sources add %s \"%s\"%s\n", c.Type, c.Path, gameFlag)
		}
	}
	return nil
}
