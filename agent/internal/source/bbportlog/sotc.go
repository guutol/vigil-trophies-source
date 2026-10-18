package bbportlog

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/vigil-tracker/agent/internal/source"
)

var ErrSOTCUnidentified = errors.New("Shadow of the Colossus: game or trophy set not identified")

// IdentifySOTC accepts only the PS4 Shadow remake. IDs come from the player's
// metadata, never from a guessed regional set or a Bloodborne default.
func IdentifySOTC(dir string) (Game, error) {
	if dir == "" {
		return Game{}, ErrSOTCUnidentified
	}
	id, title, err := sfoOf(dir)
	clean := strings.ToLower(strings.NewReplacer("®", "", "™", "").Replace(title))
	if err != nil || !strings.HasPrefix(id, "CUSA") ||
		(id != "CUSA08809" && !strings.Contains(clean, "shadow of the colossus")) {
		return Game{}, fmt.Errorf("%w: expected the PS4 Shadow of the Colossus dump", ErrSOTCUnidentified)
	}
	g, err := Identify(dir)
	if err != nil || g.TrophySetID == "" {
		return Game{}, fmt.Errorf("%w: readable sce_sys/param.sfo and npbind.dat required", ErrSOTCUnidentified)
	}
	return g, nil
}

// SOTCForUserDir reuses the proven log tailer, without Bloodborne's journal/default.
func SOTCForUserDir(dir string, game Game, idErr error) (*Source, error) {
	p, err := filepath.Abs(filepath.Join(dir, LogName))
	if err != nil {
		return nil, err
	}
	if idErr == nil && (game.TitleID == "" || game.TrophySetID == "") {
		idErr = ErrSOTCUnidentified
	}
	return &Source{name: source.SOTCLog, tail: Tail{Path: p}, game: game, blocked: idErr,
		unlocks: map[string]source.Unlock{}, Now: nowFunc}, nil
}
