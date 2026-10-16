package bbportlog

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/vigil-tracker/agent/internal/source"
)

// The God of War III Remastered port (github.com/FelipePrado0/gow3_pc) runs the PS4 game with a
// runtime that prints the same "Runtime: trophy N unlocked" line as bbport, and its launcher saves
// that output to <saves folder>/last_run.log. Unlike bbport there is no default game: the dump
// must say which game and trophy set it is, or nothing is sent.

// GOW3TitleIDs are the releases the port's launcher accepts (scripts/patches.py, windows-v2.4.0).
// A dump whose param.sfo title names God of War III is accepted too, as the launcher does.
var GOW3TitleIDs = map[string]bool{"CUSA01623": true, "CUSA01715": true, "CUSA01720": true}

const gow3Title = "God of War III"

// Why a God of War III source cannot report trophies yet. The tray app shows it and nothing is
// sent: the unlocks stay in the log and are read once the game folder can be identified.
var (
	ErrGOW3NoGameDir     = errors.New("God of War III: the game folder is not known (set it in the port's launcher)")
	ErrGOW3NoMetadata    = errors.New("God of War III: the game folder has no readable sce_sys/param.sfo")
	ErrGOW3NotGOW3       = errors.New("God of War III: the game folder holds another game")
	ErrGOW3NoTrophySet   = errors.New("God of War III: sce_sys/npbind.dat does not name one trophy set")
	errGOW3Unidentified  = errors.New("God of War III: not identified")
	gow3IdentifySentinel = []error{ErrGOW3NoGameDir, ErrGOW3NoMetadata, ErrGOW3NotGOW3, ErrGOW3NoTrophySet}
)

// IsUnidentified reports whether err says a God of War III source could not be identified.
func IsUnidentified(err error) bool {
	for _, s := range gow3IdentifySentinel {
		if errors.Is(err, s) {
			return true
		}
	}
	return errors.Is(err, errGOW3Unidentified)
}

// IdentifyGOW3 reads the dump like the launcher does: the update folder's metadata when it holds
// an eboot.bin (meta_folder in the launcher), else the game folder's. The trophy set comes from
// npbind.dat (update first, then the base game). It only accepts God of War III and never falls
// back to another game: an error says what is missing.
func IdentifyGOW3(gameDir, updateDir string) (Game, error) {
	if strings.TrimSpace(gameDir) == "" {
		return Game{}, ErrGOW3NoGameDir
	}
	meta := gameDir
	if updateDir != "" && isRegular(filepath.Join(updateDir, "eboot.bin")) {
		meta = updateDir
	}
	var g Game
	titleID, title, err := sfoOf(meta)
	if (err != nil || titleID == "") && meta != gameDir {
		titleID, title, err = sfoOf(gameDir)
	}
	if err != nil || titleID == "" {
		return Game{}, fmt.Errorf("%w (%s)", ErrGOW3NoMetadata, gameDir)
	}
	clean := strings.NewReplacer("®", "", "™", "").Replace(title)
	if !GOW3TitleIDs[titleID] && !strings.Contains(clean, gow3Title) {
		return Game{}, fmt.Errorf("%w: %s %q", ErrGOW3NotGOW3, titleID, title)
	}
	g.TitleID = titleID
	for _, dir := range []string{meta, gameDir} {
		data, err := source.ReadLimited(filepath.Join(dir, "sce_sys", "npbind.dat"), maxNPBindBytes)
		if err != nil {
			continue
		}
		if id := NPCommIDFromNPBind(data); id != "" {
			g.TrophySetID = id
			break
		}
	}
	if g.TrophySetID == "" {
		return Game{}, fmt.Errorf("%w (%s)", ErrGOW3NoTrophySet, gameDir)
	}
	return g, nil
}

func sfoOf(dir string) (titleID, title string, err error) {
	data, err := source.ReadLimited(filepath.Join(dir, "sce_sys", "param.sfo"), maxSFOBytes)
	if err != nil {
		return "", "", err
	}
	if titleID, err = TitleIDFromSFO(data); err != nil {
		return "", "", err
	}
	title, _ = SFOString(data, "TITLE", 256)
	return titleID, title, nil
}

func isRegular(p string) bool {
	info, err := os.Stat(p)
	return err == nil && info.Mode().IsRegular()
}

// NewGOW3 follows a God of War III port log. With idErr set (the game could not be identified),
// Scan reports it and neither reads the log nor returns unlocks: nothing is sent for the wrong game,
// and the lines are still there once the folder is fixed.
func NewGOW3(logPath string, game Game, idErr error) (*Source, error) {
	abs, err := filepath.Abs(logPath)
	if err != nil {
		return nil, err
	}
	if idErr == nil && game.TrophySetID == "" {
		idErr = errGOW3Unidentified
	}
	return &Source{name: source.GOW3Log, tail: Tail{Path: abs}, game: game, blocked: idErr,
		unlocks: map[string]source.Unlock{}, Now: nowFunc}, nil
}

// GOW3ForUserDir follows <saves folder>/last_run.log, as the port's launcher writes it.
func GOW3ForUserDir(userDir string, game Game, idErr error) (*Source, error) {
	return NewGOW3(filepath.Join(userDir, LogName), game, idErr)
}
