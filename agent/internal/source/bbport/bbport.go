// Package bbport reads the trophies.jsonl that the bbport patch appends to (see
// bbport-patch/FORMAT.md). The file only grows, so it is read incrementally from the last offset.
package bbport

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/vigil-tracker/agent/internal/source"
	"github.com/vigil-tracker/agent/internal/trophyfile"
)

// maxReadBytes bounds how much of the file is read per step.
const maxReadBytes = 1 << 20

// Source watches <userDir>/trophies.jsonl.
type Source struct {
	path    string
	pos     trophyfile.Position
	mtime   time.Time
	unlocks map[string]source.Unlock // by event id, so repeated lines collapse
	order   []string
	// OnInvalid, when set, is called for every skipped line (never with the line content).
	OnInvalid func(error)
}

// New returns a source for the bbport user folder (the one that holds trophies.jsonl).
func New(userDir string) (*Source, error) {
	abs, err := filepath.Abs(filepath.Join(userDir, trophyfile.FileName))
	if err != nil {
		return nil, err
	}
	return &Source{path: abs, unlocks: map[string]source.Unlock{}}, nil
}

// Name implements source.Source.
func (s *Source) Name() string { return source.BBPort }

// Path is the trophies.jsonl being watched.
func (s *Source) Path() string { return s.path }

// Scan reads the lines appended since the last call and returns every unlock seen so far.
// Unchanged files (same size and mtime) are not opened.
func (s *Source) Scan(ctx context.Context) ([]source.Unlock, error) {
	info, err := os.Stat(s.path)
	if errors.Is(err, fs.ErrNotExist) {
		return s.all(), nil // the game has not unlocked anything yet
	}
	if err != nil {
		return s.all(), err
	}
	if s.pos.Path == s.path && info.Size() == s.pos.Size && info.ModTime().Equal(s.mtime) {
		return s.all(), nil
	}
	s.mtime = info.ModTime()
	for ctx.Err() == nil {
		res, err := trophyfile.ReadNew(s.path, s.pos, maxReadBytes)
		if err != nil {
			return s.all(), fmt.Errorf("%s: %w", s.path, err)
		}
		for _, e := range res.Invalid {
			if s.OnInvalid != nil {
				s.OnInvalid(e)
			}
		}
		for _, e := range res.Entries {
			s.add(source.Unlock{Source: source.BBPort, TitleID: e.Unlock.TitleID, TrophyID: e.Unlock.TrophyID,
				UnlockedAt: e.Unlock.UnlockedAt})
		}
		progressed := res.Position.Offset != s.pos.Offset || res.Reset
		s.pos = res.Position
		if !progressed || s.pos.Offset >= s.pos.Size {
			break
		}
	}
	return s.all(), nil
}

func (s *Source) add(u source.Unlock) {
	id := source.EventID(u)
	if _, ok := s.unlocks[id]; !ok { // keep the first time seen
		s.order = append(s.order, id)
		s.unlocks[id] = u
	}
}

func (s *Source) all() []source.Unlock {
	out := make([]source.Unlock, 0, len(s.order))
	for _, id := range s.order {
		out = append(out, s.unlocks[id])
	}
	return out
}
