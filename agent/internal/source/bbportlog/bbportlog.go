// Package bbportlog reads trophies from the output of the official bbport (no patch needed).
// When the game unlocks a trophy, bbport prints exactly:
//
//	Runtime: trophy 12 unlocked
//
// On Windows the bbport launcher already saves the game's output to <user folder>/last_run.log
// (rewritten on every run), so this source just follows that file. Where there is no such file
// (the Linux launcher), "vigil-agent play bbport" starts the game itself and follows its output.
// The game folder (sce_sys/param.sfo, npbind.dat) says which game and trophy set it is.
package bbportlog

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/vigil-tracker/agent/internal/source"
)

// LogName is the file the bbport launcher writes in the user folder.
const LogName = "last_run.log"

// TrophyLogName is bbport's persistent unlock journal (id and Unix seconds).
const TrophyLogName = "trophies.log"

// ParseTrophyLine reads the journal written by bbport's trophy runtime.
func ParseTrophyLine(line string) (int, time.Time, bool) {
	fields := strings.Fields(line)
	if len(fields) != 2 {
		return 0, time.Time{}, false
	}
	id, err := strconv.Atoi(fields[0])
	if err != nil || id < 0 || id >= 128 {
		return 0, time.Time{}, false
	}
	seconds, err := strconv.ParseInt(fields[1], 10, 64)
	if err != nil {
		return 0, time.Time{}, false
	}
	when := time.Unix(seconds, 0).UTC()
	return id, when, source.FormatTime(when) != ""
}

// trophyLine matches the whole line and nothing else.
var trophyLine = regexp.MustCompile(`^Runtime: trophy (0|[1-9][0-9]{0,2}) unlocked$`)

// ParseLine returns the trophy id of a "Runtime: trophy N unlocked" line (0..255).
func ParseLine(line string) (int, bool) {
	m := trophyLine.FindStringSubmatch(line)
	if m == nil {
		return 0, false
	}
	id, err := strconv.Atoi(m[1])
	if err != nil || id > 255 {
		return 0, false
	}
	return id, true
}

// Source follows one bbport output file.
type Source struct {
	tail    Tail
	journal Tail
	game    Game
	unlocks map[string]source.Unlock
	order   []string
	// Now gives the unlock time: the moment the line was read (the log has no timestamps).
	Now func() time.Time
	// Echo, when set, receives every line read (play mode shows the game's output).
	Echo func(line string)
}

// New follows logPath. game says which trophy set/title the trophies belong to; when it is empty
// the bbport default (Bloodborne, CUSA03173) is used.
func New(logPath string, game Game) (*Source, error) {
	abs, err := filepath.Abs(logPath)
	if err != nil {
		return nil, err
	}
	if game.TrophySetID == "" && game.TitleID == "" {
		game.TitleID = DefaultTitleID
	}
	return &Source{tail: Tail{Path: abs}, journal: Tail{Path: filepath.Join(filepath.Dir(abs), TrophyLogName)}, game: game, unlocks: map[string]source.Unlock{}, Now: time.Now}, nil
}

// ForUserDir follows <userDir>/last_run.log, as the Windows launcher writes it.
func ForUserDir(userDir string, game Game) (*Source, error) {
	return New(filepath.Join(userDir, LogName), game)
}

// Name implements source.Source.
func (s *Source) Name() string { return source.BBPortLog }

// Path is the file being followed.
func (s *Source) Path() string { return s.tail.Path }

// Game is the trophy set/title the unlocks are reported for.
func (s *Source) Game() Game { return s.game }

// Scan reads the new lines and returns every unlock seen so far (the syncer drops known ones).
func (s *Source) Scan(ctx context.Context) ([]source.Unlock, error) {
	// Read persistent timestamps first; both formats share the existing event identity.
	journal, _, journalErr := s.journal.Lines()
	for _, line := range journal {
		if id, when, ok := ParseTrophyLine(line); ok {
			s.addAt(id, when)
		}
	}
	lines, _, err := s.tail.Lines()
	for _, line := range lines {
		if s.Echo != nil {
			s.Echo(line)
		}
		if id, ok := ParseLine(line); ok {
			s.add(id)
		}
	}
	if err != nil {
		err = fmt.Errorf("%s: %w", s.tail.Path, err)
	}
	if journalErr != nil {
		journalErr = fmt.Errorf("%s: %w", s.journal.Path, journalErr)
	}
	return s.all(), errors.Join(err, journalErr)
}

func (s *Source) add(id int) {
	s.addAt(id, s.Now())
}

func (s *Source) addAt(id int, when time.Time) {
	u := source.Unlock{Source: source.BBPortLog, TrophySetID: s.game.TrophySetID, TitleID: s.game.TitleID,
		TrophyID: id, UnlockedAt: source.FormatTime(when)}
	key := source.EventID(u)
	if _, ok := s.unlocks[key]; !ok {
		s.order = append(s.order, key)
		s.unlocks[key] = u
	}
}

func (s *Source) all() []source.Unlock {
	out := make([]source.Unlock, 0, len(s.order))
	for _, k := range s.order {
		out = append(out, s.unlocks[k])
	}
	return out
}
