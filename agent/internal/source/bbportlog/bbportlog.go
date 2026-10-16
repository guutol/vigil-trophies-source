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

// Source follows one runtime output file: bbport's, or the God of War III port's (NewGOW3).
type Source struct {
	name    string // the source name sent with each unlock (bbport-log or gow3-log)
	tail    Tail
	journal Tail // bbport's trophies.log; unused (empty path) for other ports
	game    Game
	blocked error // the game could not be identified: nothing is read or reported
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
	return &Source{name: source.BBPortLog, tail: Tail{Path: abs}, journal: Tail{Path: filepath.Join(filepath.Dir(abs), TrophyLogName)}, game: game, unlocks: map[string]source.Unlock{}, Now: nowFunc}, nil
}

// ForUserDir follows <userDir>/last_run.log, as the Windows launcher writes it.
func ForUserDir(userDir string, game Game) (*Source, error) {
	return New(filepath.Join(userDir, LogName), game)
}

// nowFunc is the default clock (the log lines have no timestamps).
var nowFunc = time.Now

// Name implements source.Source.
func (s *Source) Name() string {
	if s.name == "" {
		return source.BBPortLog
	}
	return s.name
}

// Unidentified is why this source reports nothing (nil when it reports normally).
func (s *Source) Unidentified() error { return s.blocked }

// Path is the file being followed.
func (s *Source) Path() string { return s.tail.Path }

// Game is the trophy set/title the unlocks are reported for.
func (s *Source) Game() Game { return s.game }

// Scan reads the new lines and returns every unlock seen so far (the syncer drops known ones).
func (s *Source) Scan(ctx context.Context) ([]source.Unlock, error) {
	if s.blocked != nil {
		return nil, s.blocked // not even read: the lines stay for when the game is identified
	}
	// Read persistent timestamps first; both formats share the existing event identity.
	var journalErr error
	if s.journal.Path != "" {
		var journal []string
		journal, _, journalErr = s.journal.Lines()
		for _, line := range journal {
			if id, when, ok := ParseTrophyLine(line); ok {
				s.addAt(id, when)
			}
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
	u := source.Unlock{Source: s.Name(), TrophySetID: s.game.TrophySetID, TitleID: s.game.TitleID,
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
