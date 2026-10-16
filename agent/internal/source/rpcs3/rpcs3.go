// Package rpcs3 reads trophy progress from an RPCS3 (PS3 emulator) installation:
//
//	<root>/dev_hdd0/home/<8-digit user>/trophy/<NPWRxxxxx_xx>/TROPUSR.DAT
//
// The trophy set id comes from the folder name. Files are only read, never written.
package rpcs3

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"sort"

	"github.com/vigil-tracker/agent/internal/catalog"
	"github.com/vigil-tracker/agent/internal/source"
)

var userDirPattern = regexp.MustCompile(`^[0-9]{8}$`)

// Source scans every trophy set of every RPCS3 user under one root.
type Source struct {
	root  string
	cache source.Cache
}

// New accepts the RPCS3 folder (with dev_hdd0), dev_hdd0 itself, a home/<user> folder or a
// trophy folder; see TrophyDirs.
func New(root string) (*Source, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	return &Source{root: abs}, nil
}

// Name implements source.Source.
func (s *Source) Name() string { return source.RPCS3 }

// Root is the folder given to New.
func (s *Source) Root() string { return s.root }

// TrophyDirs finds the "trophy" folders under root without walking the disk: only the fixed
// RPCS3 layout is tried.
func TrophyDirs(root string) []string {
	var dirs []string
	add := func(p string) {
		if isDir(p) {
			dirs = append(dirs, p)
		}
	}
	homes := func(home string) {
		entries, _ := os.ReadDir(home)
		for _, e := range entries {
			if e.IsDir() && userDirPattern.MatchString(e.Name()) {
				add(filepath.Join(home, e.Name(), "trophy"))
			}
		}
	}
	switch {
	case isDir(filepath.Join(root, "dev_hdd0", "home")):
		homes(filepath.Join(root, "dev_hdd0", "home"))
	case isDir(filepath.Join(root, "home")):
		homes(filepath.Join(root, "home"))
	case isDir(filepath.Join(root, "trophy")):
		add(filepath.Join(root, "trophy"))
	case filepath.Base(root) == "trophy":
		add(root)
	}
	return dirs
}

// Scan parses each TROPUSR.DAT that changed since the last call.
func (s *Source) Scan(ctx context.Context) ([]source.Unlock, error) {
	var out []source.Unlock
	var errs []error
	for _, dir := range TrophyDirs(s.root) {
		entries, err := os.ReadDir(dir)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
		for _, e := range entries {
			if ctx.Err() != nil {
				return out, ctx.Err()
			}
			set := e.Name()
			if !e.IsDir() || !source.TrophySetIDPattern.MatchString(set) {
				continue
			}
			unlocks, err := s.cache.Get(filepath.Join(dir, set, "TROPUSR.DAT"), func(path string) ([]source.Unlock, error) {
				return parseFile(path, set)
			})
			if err != nil {
				errs = append(errs, err)
			}
			out = append(out, unlocks...)
		}
	}
	return out, errors.Join(errs...)
}

func parseFile(path, set string) ([]source.Unlock, error) {
	data, err := source.ReadLimited(path, MaxFileSize)
	if err != nil {
		return nil, err
	}
	trophies, err := ParseTROPUSR(data)
	if err != nil {
		return nil, err
	}
	var out []source.Unlock
	for _, t := range trophies {
		if t.Unlocked {
			out = append(out, source.Unlock{Source: source.RPCS3, TrophySetID: set, TrophyID: t.ID,
				UnlockedAt: source.FormatTime(t.Time)})
		}
	}
	return out, nil
}

func isDir(p string) bool {
	info, err := os.Stat(p)
	return err == nil && info.IsDir()
}

// CatalogDirs lists the installed trophy sets that have the plain trophy data RPCS3 keeps
// (TROPCONF.SFM), for catalog proposals.
func (s *Source) CatalogDirs() []catalog.Dir {
	var out []catalog.Dir
	for _, dir := range TrophyDirs(s.root) {
		entries, _ := os.ReadDir(dir)
		for _, e := range entries {
			set := e.Name()
			if e.IsDir() && source.TrophySetIDPattern.MatchString(set) &&
				fileExists(filepath.Join(dir, set, "TROPCONF.SFM")) {
				out = append(out, catalog.Dir{SetID: set, Kind: catalog.KindRPCS3, Path: filepath.Join(dir, set)})
			}
		}
	}
	return out
}

func fileExists(p string) bool {
	info, err := os.Stat(p)
	return err == nil && info.Mode().IsRegular()
}
