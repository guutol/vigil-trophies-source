package detect

import (
	"path/filepath"
	"runtime"
	"testing"

	"github.com/vigil-tracker/agent/internal/source"
)

// gow3Package lays out a synthetic Windows package of the port (gow3-windows), as package.sh builds
// it: the launcher, the game player, bin\gow3-probe.exe and the patch file. Nothing is executable.
func gow3Package(t *testing.T, dir string) string {
	t.Helper()
	for _, f := range []string{"God of War III.exe", "Play God of War III.exe", filepath.Join("bin", "gow3-probe.exe"),
		filepath.Join("patches", "God_of_War_III_Remastered.xml"), "run.py"} {
		write(t, filepath.Join(dir, f), "synthetic")
	}
	return dir
}

func settingsJSON(gameDir, updateDir, userDir string) string {
	return `{"ui_language": "pt", "game_dir": ` + jsonString(gameDir) + `, "update_dir": ` + jsonString(updateDir) +
		`, "user_dir": ` + jsonString(userDir) + `, "close_on_play": false}`
}

func byPath(cands []Candidate) map[string]Candidate {
	out := map[string]Candidate{}
	for _, c := range cands {
		out[c.Path] = c
	}
	return out
}

func TestGOW3PackageFoundBeforeTheFirstRun(t *testing.T) {
	home := t.TempDir()
	// the official zip extracted in Downloads, the game next to it (the launcher's default), never run:
	// no user folder and no last_run.log yet
	games := filepath.Join(home, "Downloads")
	pkg := gow3Package(t, filepath.Join(games, GOW3PackageName))
	game := filepath.Join(games, "CUSA01623")
	mkdirs(t, filepath.Join(game, "sce_sys"))
	got := byPath(Find(Env{Home: home}))
	c, ok := got[filepath.Join(pkg, "user")]
	if !ok || c.Type != source.GOW3Log || !c.Certain || c.GameDir != game {
		t.Fatalf("package before the first run: %+v (all: %+v)", c, got)
	}
	if len(got) != 1 {
		t.Fatalf("only the port's saves folder: %+v", got)
	}
}

func TestGOW3LauncherSettingsNameTheFolders(t *testing.T) {
	home, appData := t.TempDir(), t.TempDir()
	// a custom saves folder and a game on "another disk" (any folder outside the usual places)
	other := t.TempDir()
	saves := filepath.Join(other, "GoW saves")
	game := filepath.Join(other, "dumps", "CUSA01623")
	update := filepath.Join(other, "dumps", "CUSA01623-patch")
	mkdirs(t, saves, game, update)
	write(t, filepath.Join(appData, "gow3-launcher", "settings.json"), settingsJSON(game, update, saves))
	got := byPath(Find(Env{Home: home, AppData: appData}))
	c := got[saves]
	if c.Type != source.GOW3Log || c.GameDir != game || c.UpdateDir != update || !c.Certain {
		t.Fatalf("custom saves folder: %+v (all: %+v)", c, got)
	}
}

func TestGOW3EmptyUserDirUsesThePackageNextToTheGame(t *testing.T) {
	home, appData := t.TempDir(), t.TempDir()
	// user_dir empty: the log goes to <package>/user. The package is next to the configured game folder
	// on a disk detection does not browse otherwise.
	disk := t.TempDir()
	game := filepath.Join(disk, "Games", "CUSA01623")
	mkdirs(t, game)
	pkg := gow3Package(t, filepath.Join(disk, "Games", "God of War III port"))
	write(t, filepath.Join(appData, "gow3-launcher", "settings.json"), settingsJSON(game, "", ""))
	got := byPath(Find(Env{Home: home, AppData: appData}))
	c := got[filepath.Join(pkg, "user")]
	if c.Type != source.GOW3Log || c.GameDir != game {
		t.Fatalf("package beside the game: %+v (all: %+v)", c, got)
	}
}

func TestGOW3RunningPortAndDataDir(t *testing.T) {
	home := t.TempDir()
	pkg := gow3Package(t, filepath.Join(t.TempDir(), "anywhere", "gow3"))
	data := t.TempDir()
	got := byPath(Find(Env{Home: home, RunningGOW3: []string{pkg}, GOW3DataDir: data}))
	if got[filepath.Join(pkg, "user")].Type != source.GOW3Log {
		t.Errorf("running port: %+v", got)
	}
	if got[filepath.Join(data, "user")].Type != source.GOW3Log {
		t.Errorf("GOW3_DATA_DIR: %+v", got)
	}
}

func TestGOW3AppImageDataFolder(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the AppImage is Linux only")
	}
	home := t.TempDir()
	mkdirs(t, filepath.Join(home, ".local", "share", "gow3"))
	got := byPath(Find(Env{Home: home}))
	if got[filepath.Join(home, ".local", "share", "gow3", "user")].Type != source.GOW3Log {
		t.Fatalf("AppImage data folder: %+v", got)
	}
}

// A God of War III saves folder holds "Runtime: trophy N unlocked" lines like bbport's: it must be the
// port's, never Bloodborne's, and bbport's game folder must not be given to it.
func TestGOW3IsNeverTakenForBBPort(t *testing.T) {
	home, appData := t.TempDir(), t.TempDir()
	pkg := gow3Package(t, filepath.Join(home, "Desktop", GOW3PackageName))
	write(t, filepath.Join(pkg, "user", "last_run.log"), "Runtime: trophy 3 unlocked\r\r\n")
	// bbport is installed too, with its own launcher and game folder
	bbUser := filepath.Join(home, "bb", "user")
	bbGame := filepath.Join(home, "bb", "CUSA03173")
	mkdirs(t, bbUser, bbGame)
	write(t, filepath.Join(home, "bb", "bbport.ini"), "")
	write(t, filepath.Join(appData, "bbport-launcher", "settings.json"),
		`{"user_dir": `+jsonString(bbUser)+`, "game_dir": `+jsonString(bbGame)+`}`)
	got := byPath(Find(Env{Home: home, AppData: appData}))
	gow := got[filepath.Join(pkg, "user")]
	if gow.Type != source.GOW3Log || gow.GameDir == bbGame {
		t.Fatalf("God of War III folder: %+v", gow)
	}
	for _, c := range got {
		if c.Type == source.BBPortLog && c.Path == filepath.Join(pkg, "user") {
			t.Fatalf("the port's folder was also taken for bbport: %+v", got)
		}
	}
	if bb := got[bbUser]; bb.Type != source.BBPortLog || bb.GameDir != bbGame {
		t.Fatalf("bbport keeps its own folder and game: %+v", bb)
	}
	// picked by hand ("Add folder"): the port's folder or its saves folder
	for _, dir := range []string{pkg, filepath.Join(pkg, "user")} {
		cands := classify(dir, GOW3Settings{})
		if len(cands) != 1 || cands[0].Type != source.GOW3Log || cands[0].Path != filepath.Join(pkg, "user") {
			t.Fatalf("classify %s: %+v", dir, cands)
		}
	}
}

func TestGOW3NotFoundWithoutEvidence(t *testing.T) {
	home := t.TempDir()
	// a folder called gow3-windows with nothing of the port, and a dump folder alone
	mkdirs(t, filepath.Join(home, "Downloads", GOW3PackageName, "user"), filepath.Join(home, "Downloads", "CUSA01623", "sce_sys"))
	for _, c := range Find(Env{Home: home}) {
		if c.Type == source.GOW3Log {
			t.Fatalf("no evidence of the port, yet found: %+v", c)
		}
	}
}
