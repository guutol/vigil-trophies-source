package bbportlog

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/vigil-tracker/agent/internal/source"
)

// Synthetic dumps only: the set ids below are made up for the tests (NPWR9xxxx), not real catalog ids.
const (
	fakeGOW3Set   = "NPWR90301_00"
	fakeGOW3Patch = "NPWR90302_00"
)

// dump writes a synthetic game folder: sce_sys/param.sfo (title id and title) and, when set is not
// empty, sce_sys/npbind.dat naming it. eboot adds an eboot.bin (an update folder has one).
func dump(t *testing.T, dir, titleID, title, set string, eboot bool) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, "sce_sys"), 0o700); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(dir, "sce_sys", "param.sfo"), sfo(map[string]string{"TITLE_ID": titleID, "TITLE": title}), 0o600)
	if set != "" {
		os.WriteFile(filepath.Join(dir, "sce_sys", "npbind.dat"), []byte("\x00\x02"+set+"\x00rest"), 0o600)
	}
	if eboot {
		os.WriteFile(filepath.Join(dir, "eboot.bin"), []byte("synthetic"), 0o600)
	}
	return dir
}

func TestIdentifyGOW3ReadsTheDumpLikeTheLauncher(t *testing.T) {
	base := dump(t, t.TempDir(), "CUSA01623", "God of War® III Remastered", fakeGOW3Set, true)
	g, err := IdentifyGOW3(base, "")
	if err != nil || g.TrophySetID != fakeGOW3Set || g.TitleID != "CUSA01623" {
		t.Fatalf("base game: %+v %v", g, err)
	}
	// an update folder with an eboot.bin is the launcher's metadata folder (meta_folder)
	update := dump(t, t.TempDir(), "CUSA01715", "God of War III Remastered", fakeGOW3Patch, true)
	if g, err := IdentifyGOW3(base, update); err != nil || g.TitleID != "CUSA01715" || g.TrophySetID != fakeGOW3Patch {
		t.Fatalf("with update: %+v %v", g, err)
	}
	// an update without npbind.dat: the base game's trophy set
	bare := dump(t, t.TempDir(), "CUSA01623", "God of War III Remastered", "", true)
	if g, err := IdentifyGOW3(base, bare); err != nil || g.TrophySetID != fakeGOW3Set {
		t.Fatalf("update without npbind: %+v %v", g, err)
	}
	// an update folder without an eboot.bin is ignored, as the launcher does
	noEboot := dump(t, t.TempDir(), "CUSA99999", "Other", "", false)
	if g, err := IdentifyGOW3(base, noEboot); err != nil || g.TitleID != "CUSA01623" {
		t.Fatalf("update without eboot: %+v %v", g, err)
	}
	// another release id is accepted when the title names the game (the launcher does the same)
	other := dump(t, t.TempDir(), "CUSA77777", "God of War III Remastered", fakeGOW3Set, true)
	if g, err := IdentifyGOW3(other, ""); err != nil || g.TitleID != "CUSA77777" {
		t.Fatalf("title match: %+v %v", g, err)
	}
}

func TestIdentifyGOW3NeverFallsBackToAnotherGame(t *testing.T) {
	cases := map[string]struct {
		dir  func(t *testing.T) string
		want error
	}{
		"no game folder": {func(*testing.T) string { return "" }, ErrGOW3NoGameDir},
		"empty folder":   {func(t *testing.T) string { return t.TempDir() }, ErrGOW3NoMetadata},
		"bloodborne dump": {func(t *testing.T) string {
			return dump(t, t.TempDir(), "CUSA03173", "Bloodborne", "NPWR05818_00", true)
		}, ErrGOW3NotGOW3},
		"no npbind": {func(t *testing.T) string {
			return dump(t, t.TempDir(), "CUSA01623", "God of War III Remastered", "", true)
		}, ErrGOW3NoTrophySet},
		"two sets in npbind": {func(t *testing.T) string {
			d := dump(t, t.TempDir(), "CUSA01623", "God of War III Remastered", "", true)
			os.WriteFile(filepath.Join(d, "sce_sys", "npbind.dat"), []byte(fakeGOW3Set+"\x00"+fakeGOW3Patch), 0o600)
			return d
		}, ErrGOW3NoTrophySet},
	}
	for name, c := range cases {
		g, err := IdentifyGOW3(c.dir(t), "")
		if !errors.Is(err, c.want) || g != (Game{}) || !IsUnidentified(err) {
			t.Errorf("%s: got %+v, %v; want %v", name, g, err, c.want)
		}
	}
}

func gow3Fixture(t *testing.T, game Game, idErr error) *fixture {
	t.Helper()
	dir := t.TempDir()
	src, err := GOW3ForUserDir(dir, game, idErr)
	if err != nil {
		t.Fatal(err)
	}
	f := &fixture{t: t, path: filepath.Join(dir, LogName), src: src, now: time.Date(2026, 10, 16, 21, 0, 0, 0, time.UTC)}
	src.Now = func() time.Time { return f.now }
	return f
}

func TestGOW3LogIsReportedForItsOwnSet(t *testing.T) {
	game := Game{TrophySetID: fakeGOW3Set, TitleID: "CUSA01623"}
	f := gow3Fixture(t, game, nil)
	// nothing yet: the launcher creates last_run.log when the game starts
	if got := f.ids(); len(got) != 0 {
		t.Fatalf("before the first run: %v", got)
	}
	// Windows: the launcher writes the pipe's CR LF through a text-mode file, so lines end in CR CR LF
	f.write("GOW3 runtime\r\r\nRuntime: trophy 3 unlocked\r\r\nRuntime: trophy 3 unlocked\r\r\nRuntime: troph", false)
	if got := f.ids(); len(got) != 1 || got[0] != 3 {
		t.Fatalf("first lines (duplicate and partial line): %v", got)
	}
	f.write("y 7 unlocked\r\r\n", true) // the rest of the partial line
	units, err := f.src.Scan(context.Background())
	if err != nil || len(units) != 2 || units[1].TrophyID != 7 {
		t.Fatalf("completed line: %+v %v", units, err)
	}
	for _, u := range units {
		if u.Source != source.GOW3Log || u.TrophySetID != fakeGOW3Set || u.TitleID != "CUSA01623" || u.UnlockedAt == "" {
			t.Fatalf("unlock: %+v", u)
		}
	}
	// a new game session rewrites the log ('w'): read from the start, nothing reported twice
	f.write("Runtime: trophy 7 unlocked\r\r\nRuntime: trophy 12 unlocked\r\r\n", false)
	if got := f.ids(); len(got) != 3 || got[2] != 12 {
		t.Fatalf("rewritten log: %v", got)
	}
	// a shorter log replacing a longer one (truncated) is read from the start too
	f.write("Runtime: trophy 20 unlocked\n", false)
	if got := f.ids(); len(got) != 4 || got[3] != 20 {
		t.Fatalf("truncated log: %v", got)
	}
}

func TestGOW3IgnoresBloodbornesJournal(t *testing.T) {
	f := gow3Fixture(t, Game{TrophySetID: fakeGOW3Set, TitleID: "CUSA01623"}, nil)
	os.WriteFile(filepath.Join(filepath.Dir(f.path), TrophyLogName), []byte("5 1791669868\n"), 0o600)
	if got := f.ids(); len(got) != 0 {
		t.Fatalf("a trophies.log next to the God of War III log is not this game's journal: %v", got)
	}
}

func TestUnidentifiedGOW3SendsNothingAndLosesNothing(t *testing.T) {
	f := gow3Fixture(t, Game{}, ErrGOW3NoGameDir)
	f.write("Runtime: trophy 4 unlocked\n", false)
	got, err := f.src.Scan(context.Background())
	if len(got) != 0 || !errors.Is(err, ErrGOW3NoGameDir) || !IsUnidentified(f.src.Unidentified()) {
		t.Fatalf("unidentified: %+v %v", got, err)
	}
	// nothing was consumed: once identified (a new reader, as the app opens it), the line is there
	src, _ := GOW3ForUserDir(filepath.Dir(f.path), Game{TrophySetID: fakeGOW3Set, TitleID: "CUSA01623"}, nil)
	units, err := src.Scan(context.Background())
	if err != nil || len(units) != 1 || units[0].TrophyID != 4 || units[0].TrophySetID != fakeGOW3Set {
		t.Fatalf("after identification: %+v %v", units, err)
	}
	// and an empty game never becomes Bloodborne
	empty, _ := NewGOW3(f.path, Game{}, nil)
	if got, err := empty.Scan(context.Background()); len(got) != 0 || !IsUnidentified(err) {
		t.Fatalf("empty game: %+v %v", got, err)
	}
}

func TestTwoPortsStayApart(t *testing.T) {
	bb := newFixture(t, Game{TrophySetID: "NPWR05818_00", TitleID: "CUSA03173"})
	gow := gow3Fixture(t, Game{TrophySetID: fakeGOW3Set, TitleID: "CUSA01623"}, nil)
	bb.write("Runtime: trophy 5 unlocked\n", false)
	gow.write("Runtime: trophy 5 unlocked\n", false)
	a, _ := bb.src.Scan(context.Background())
	b, _ := gow.src.Scan(context.Background())
	if len(a) != 1 || len(b) != 1 || a[0].Source != source.BBPortLog || b[0].Source != source.GOW3Log ||
		a[0].TrophySetID == b[0].TrophySetID || source.EventID(a[0]) == source.EventID(b[0]) {
		t.Fatalf("same trophy id in two games: %+v / %+v", a, b)
	}
}
