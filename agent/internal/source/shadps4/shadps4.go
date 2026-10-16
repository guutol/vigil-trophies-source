// Package shadps4 reads trophy progress from a shadPS4 (PS4 emulator) user folder. Current
// versions keep one file per trophy set and user:
//
//	<user>/home/<user id>/trophy/<NPWRxxxxx_xx>.xml
//
// Older versions wrote the unlock state into the game's copy of the trophy data:
//
//	<user>/game_data/<TITLE_ID>/TrophyFiles/trophy00/Xml/TROP.XML   (set id in <npcommid>)
//
// Both are read; files are never written. shadPS4 only has these XML files after it decrypted
// the game's trophy data with the key the user configured in shadPS4 itself: the agent never
// handles keys.
package shadps4

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/vigil-tracker/agent/internal/catalog"
	"github.com/vigil-tracker/agent/internal/source"
)

var userIDPattern = regexp.MustCompile(`^[0-9]{1,10}$`)

// Source scans one shadPS4 user folder.
type Source struct {
	user  string
	cache source.Cache
}

// New accepts the shadPS4 "user" folder or the emulator folder that contains it.
func New(path string) (*Source, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	return &Source{user: UserDir(abs)}, nil
}

// UserDir returns path/user when it exists, otherwise path.
func UserDir(path string) string {
	if isDir(filepath.Join(path, "user")) && !isDir(filepath.Join(path, "home")) {
		return filepath.Join(path, "user")
	}
	return path
}

// Name implements source.Source.
func (s *Source) Name() string { return source.ShadPS4 }

// Root is the user folder being read.
func (s *Source) Root() string { return s.user }

// Scan parses the trophy files that changed since the last call.
func (s *Source) Scan(ctx context.Context) ([]source.Unlock, error) {
	var out []source.Unlock
	var errs []error
	for _, f := range s.files() {
		if ctx.Err() != nil {
			return out, ctx.Err()
		}
		f := f
		unlocks, err := s.cache.Get(f.path, func(path string) ([]source.Unlock, error) { return parseFile(f) })
		if err != nil {
			errs = append(errs, err)
		}
		out = append(out, unlocks...)
	}
	return out, errors.Join(errs...)
}

// candidate is a trophy file plus what its location says about it.
type candidate struct {
	path    string
	setID   string // from the file name (current layout)
	titleID string // from the folder name (old layout)
}

func (s *Source) files() []candidate {
	var out []candidate
	home := filepath.Join(s.user, "home")
	users, _ := os.ReadDir(home)
	for _, u := range users {
		if !u.IsDir() || !userIDPattern.MatchString(u.Name()) {
			continue
		}
		dir := filepath.Join(home, u.Name(), "trophy")
		entries, _ := os.ReadDir(dir)
		for _, e := range entries {
			set := strings.TrimSuffix(e.Name(), ".xml")
			if !e.IsDir() && strings.HasSuffix(e.Name(), ".xml") && source.TrophySetIDPattern.MatchString(set) {
				out = append(out, candidate{path: filepath.Join(dir, e.Name()), setID: set})
			}
		}
	}
	games, _ := os.ReadDir(filepath.Join(s.user, "game_data"))
	for _, g := range games {
		if g.IsDir() && source.TitleIDPattern.MatchString(g.Name()) {
			p := filepath.Join(s.user, "game_data", g.Name(), "TrophyFiles", "trophy00", "Xml", "TROP.XML")
			if isFile(p) {
				out = append(out, candidate{path: p, titleID: g.Name()})
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].path < out[j].path })
	return out
}

func parseFile(c candidate) ([]source.Unlock, error) {
	data, err := source.ReadLimited(c.path, MaxFileSize)
	if err != nil {
		return nil, err
	}
	f, err := ParseTrophyXML(data)
	if err != nil {
		return nil, err
	}
	set := c.setID
	if set == "" && source.TrophySetIDPattern.MatchString(f.NPCommID) {
		set = f.NPCommID
	}
	if c.setID != "" && f.NPCommID != "" && f.NPCommID != c.setID {
		return nil, errors.New("npcommid does not match the file name")
	}
	var out []source.Unlock
	for _, t := range f.Trophies {
		if t.Unlocked {
			out = append(out, source.Unlock{Source: source.ShadPS4, TrophySetID: set, TitleID: c.titleID,
				TrophyID: t.ID, UnlockedAt: source.FormatTime(t.Time)})
		}
	}
	return out, nil
}

func isDir(p string) bool {
	info, err := os.Stat(p)
	return err == nil && info.IsDir()
}

func isFile(p string) bool {
	info, err := os.Stat(p)
	return err == nil && info.Mode().IsRegular()
}

// CatalogDirs lists the trophy folders shadPS4 extracted (<user>/trophy/<NPWR>/Xml), for catalog
// proposals. They only exist when shadPS4 itself could read the game's trophy data.
func (s *Source) CatalogDirs() []catalog.Dir {
	var out []catalog.Dir
	root := filepath.Join(s.user, "trophy")
	entries, _ := os.ReadDir(root)
	for _, e := range entries {
		set := e.Name()
		if e.IsDir() && source.TrophySetIDPattern.MatchString(set) && isDir(filepath.Join(root, set, "Xml")) {
			out = append(out, catalog.Dir{SetID: set, Kind: catalog.KindShadPS4, Path: filepath.Join(root, set)})
		}
	}
	return out
}
