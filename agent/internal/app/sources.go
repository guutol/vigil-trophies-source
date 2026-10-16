// Package app holds what the command line (vigil-agent) and the tray app (vigil-agent-tray)
// share: opening the configured sources, summarizing what they hold, the device-flow wait and the
// background sync engine.
package app

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/vigil-tracker/agent/internal/api"
	"github.com/vigil-tracker/agent/internal/catalog"
	"github.com/vigil-tracker/agent/internal/config"
	"github.com/vigil-tracker/agent/internal/source"
	"github.com/vigil-tracker/agent/internal/source/bbport"
	"github.com/vigil-tracker/agent/internal/source/bbportlog"
	"github.com/vigil-tracker/agent/internal/source/detect"
	"github.com/vigil-tracker/agent/internal/source/rpcs3"
	"github.com/vigil-tracker/agent/internal/source/shadps4"
	"github.com/vigil-tracker/agent/internal/syncer"
)

// OpenSource builds the reader for one configured source. logger may be nil.
func OpenSource(c config.SourceConfig, logger *log.Logger) (source.Source, error) {
	switch c.Type {
	case source.BBPortLog:
		return bbportlog.ForUserDir(c.Path, Identify(c.GameDir, logger))
	case source.GOW3Log:
		game, err := IdentifyGOW3(c)
		if err != nil && logger != nil {
			logger.Printf("gow3-log %s: %v; nothing is sent until the game folder is identified", c.Path, err)
		}
		return bbportlog.GOW3ForUserDir(c.Path, game, err)
	case source.BBPort:
		s, err := bbport.New(c.Path)
		if err == nil && logger != nil {
			s.OnInvalid = func(e error) { logger.Printf("bbport: skipped an invalid line: %v", e) }
		}
		return s, err
	case source.RPCS3:
		return rpcs3.New(c.Path)
	case source.ShadPS4:
		return shadps4.New(c.Path)
	}
	return nil, fmt.Errorf("unknown source type %q (use %s)", c.Type, strings.Join(source.Types, ", "))
}

// OpenSources opens every source once (duplicates removed).
func OpenSources(configs []config.SourceConfig, logger *log.Logger) ([]source.Source, error) {
	var srcs []source.Source
	for _, c := range Dedupe(configs) {
		src, err := OpenSource(c, logger)
		if err != nil {
			return nil, fmt.Errorf("source %s %q: %w", c.Type, c.Path, err)
		}
		if logger != nil {
			logger.Printf("watching %s: %s", c.Type, c.Path)
		}
		srcs = append(srcs, src)
	}
	return srcs, nil
}

// SourceKey identifies a source by type and folder (case-insensitive, like Windows paths).
func SourceKey(c config.SourceConfig) string {
	return c.Type + "|" + strings.ToLower(filepath.Clean(c.Path))
}

// Dedupe keeps the first of each type+folder.
func Dedupe(configs []config.SourceConfig) []config.SourceConfig {
	var out []config.SourceConfig
	seen := map[string]bool{}
	for _, c := range configs {
		if !seen[SourceKey(c)] {
			seen[SourceKey(c)] = true
			out = append(out, c)
		}
	}
	return out
}

// SameFile compares two paths the way Windows does (b == "" never matches).
func SameFile(a, b string) bool {
	return b != "" && strings.EqualFold(filepath.Clean(a), filepath.Clean(b))
}

// PlayLogPath is where "play" saves the game's output: the agent's own folder, never the port's.
func PlayLogPath(store *config.Store) string {
	return filepath.Join(store.Dir, "play", "bbport.log")
}

// ConfiguredGameDir is the game folder of the first configured bbport-log source.
func ConfiguredGameDir(store *config.Store) string {
	for _, c := range store.Sources() {
		if c.Type == source.BBPortLog && c.GameDir != "" {
			return c.GameDir
		}
	}
	return ""
}

// Identify reads the game folder; without one, bbport's default (Bloodborne) is used.
func Identify(gameDir string, logger *log.Logger) bbportlog.Game {
	if gameDir == "" {
		if logger != nil {
			logger.Printf("bbport-log: no game folder configured; reporting trophies for %s", bbportlog.DefaultTitleID)
		}
		return bbportlog.Game{}
	}
	game, err := bbportlog.Identify(gameDir)
	if err != nil && logger != nil {
		logger.Printf("bbport-log: %v", err)
	}
	return game
}

// IdentifyGOW3 says which game and trophy set a God of War III port folder reports for: the saved
// game folder, or, when that no longer identifies the game (moved, renamed), the one the port's
// launcher is set to now. Never Bloodborne: without an identification the source sends nothing.
func IdentifyGOW3(c config.SourceConfig) (bbportlog.Game, error) {
	game, err := bbportlog.IdentifyGOW3(c.GameDir, c.UpdateDir)
	if err == nil {
		return game, nil
	}
	if live := detect.ReadGOW3Settings(detect.FromOSLight()); live.Found && live.GameDir != "" &&
		(live.GameDir != c.GameDir || live.UpdateDir != c.UpdateDir) {
		if g, err2 := bbportlog.IdentifyGOW3(live.GameDir, live.UpdateDir); err2 == nil {
			return g, nil
		}
	}
	return bbportlog.Game{}, err
}

// Proposer is the community-catalog sender, or nil when the user turned it off.
func Proposer(client *api.Client, store *config.Store, srcs []source.Source, logger *log.Logger) *syncer.Proposer {
	if !store.CatalogProposalsEnabled() {
		return nil
	}
	var providers []catalog.Provider
	for _, s := range srcs {
		if p, ok := s.(catalog.Provider); ok {
			providers = append(providers, p)
		}
	}
	if len(providers) == 0 {
		return nil
	}
	return syncer.NewProposer(client, store, providers, logger)
}

// FileExists is true for a regular file.
func FileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}

// Summary is what one source holds: how many games (trophy sets or title ids) and unlocks.
type Summary struct {
	Config  config.SourceConfig
	Games   int
	Unlocks int
	// PerGame: "NPWR05818_00: 12 unlocked"-style lines, sorted.
	PerGame []string
	// Err: the source could not be opened, or some files could not be read.
	Err error
}

// Summarize opens a separate reader for c (so it never races with a running sync) and counts
// what it holds. Read only; nothing is sent.
func Summarize(ctx context.Context, c config.SourceConfig) Summary {
	sum := Summary{Config: c}
	src, err := OpenSource(c, nil)
	if err != nil {
		sum.Err = err
		return sum
	}
	scanCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	unlocks, err := src.Scan(scanCtx)
	sum.Err = err
	sum.Unlocks = len(unlocks)
	sum.PerGame = CountPerGame(unlocks)
	sum.Games = len(sum.PerGame)
	return sum
}

// CountPerGame counts unlocks per trophy set (or title id), one line each, sorted.
func CountPerGame(unlocks []source.Unlock) []string {
	counts := map[string]int{}
	for _, u := range unlocks {
		key := u.TrophySetID
		switch {
		case key == "":
			key = u.TitleID + " (title id)"
		case u.TitleID != "":
			key += " / " + u.TitleID
		}
		counts[key]++
	}
	keys := make([]string, 0, len(counts))
	for k := range counts {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	lines := make([]string, 0, len(keys))
	for _, k := range keys {
		lines = append(lines, fmt.Sprintf("%s: %d unlocked", k, counts[k]))
	}
	return lines
}
