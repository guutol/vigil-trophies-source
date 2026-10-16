package detect

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/vigil-tracker/agent/internal/source"
)

func TestJournalOnlyBBPortDetection(t *testing.T) {
	port := t.TempDir()
	user := filepath.Join(port, "user")
	mkdirs(t, user)
	write(t, filepath.Join(user, "trophies.log"), "29 1791669868\n")
	if len(Classify(user)) != 0 {
		t.Fatal("numeric file alone is not bbport")
	}
	write(t, filepath.Join(port, "bbport.ini"), "[General]\n")
	got := Classify(port)
	if len(got) != 1 || got[0].Type != source.BBPortLog || got[0].Path != user {
		t.Fatalf("journal-only detection: %+v", got)
	}
}

func mkdirs(t *testing.T, paths ...string) {
	t.Helper()
	for _, p := range paths {
		if err := os.MkdirAll(p, 0o700); err != nil {
			t.Fatal(err)
		}
	}
}

func TestFindLooksOnlyInObviousPlaces(t *testing.T) {
	home := t.TempDir()
	appData := filepath.Join(home, "AppData", "Roaming")
	pf := filepath.Join(home, "ProgramFiles")

	rp := filepath.Join(home, "Downloads", "rpcs3-v0.0.30")
	mkdirs(t, filepath.Join(rp, "dev_hdd0", "home", "00000001"))
	shadDesktop := filepath.Join(home, "OneDrive", "Desktop", "Some emulator folder")
	mkdirs(t, filepath.Join(shadDesktop, "home"), filepath.Join(shadDesktop, "sys_modules"))
	shadPF := filepath.Join(pf, "shadPS4")
	mkdirs(t, filepath.Join(shadPF, "user", "home"), filepath.Join(shadPF, "user", "trophy"))
	mkdirs(t, filepath.Join(appData, "rpcs3", "dev_hdd0", "home"))
	bbUser := filepath.Join(home, "ports", "bb", "user")
	bbGame := filepath.Join(home, "ports", "CUSA00001")
	mkdirs(t, bbUser, bbGame, filepath.Join(appData, "bbport-launcher"))
	os.WriteFile(filepath.Join(appData, "bbport-launcher", "settings.json"),
		[]byte(`{"user_dir":`+jsonString(bbUser)+`,"game_dir":`+jsonString(bbGame)+`}`), 0o600)
	// a folder with the official bbport launcher's log (no patch)
	logDir := filepath.Join(home, "Desktop", "bb-user")
	mkdirs(t, logDir)
	os.WriteFile(filepath.Join(logDir, "last_run.log"), []byte("Runtime: trophy 1 unlocked\n"), 0o600)
	// two levels down: must NOT be found (no disk walk)
	mkdirs(t, filepath.Join(home, "Desktop", "a", "b", "dev_hdd0", "home"))
	// a folder with only "home" is not enough for shadPS4
	mkdirs(t, filepath.Join(home, "Documents", "random", "home"))

	got := Find(Env{Home: home, AppData: appData, ProgramFiles: []string{pf}})
	want := map[Candidate]bool{
		{source.BBPortLog, bbUser, bbGame, true, ""}:                  true, // the launcher's settings say so
		{source.BBPortLog, logDir, bbGame, false, ""}:                 true, // only the log says so; the launcher's game folder
		{source.RPCS3, rp, "", true, ""}:                              true,
		{source.RPCS3, filepath.Join(appData, "rpcs3"), "", true, ""}: true,
		{source.ShadPS4, shadDesktop, "", true, ""}:                   true,
		{source.ShadPS4, filepath.Join(shadPF, "user"), "", true, ""}: true,
	}
	if len(got) != len(want) {
		t.Fatalf("got %v", got)
	}
	for _, c := range got {
		if !want[c] {
			t.Errorf("unexpected %v", c)
		}
	}
}

func TestExtraFoldersAreChecked(t *testing.T) {
	extra := t.TempDir()
	mkdirs(t, filepath.Join(extra, "dev_hdd0", "home"))
	got := Find(Env{Extra: []string{extra}})
	if len(got) != 1 || got[0].Type != source.RPCS3 || got[0].Path != extra || got[0].GameDir != "" {
		t.Fatalf("got %v", got)
	}
}

func jsonString(s string) string {
	out := []byte{'"'}
	for _, r := range s {
		if r == '\\' || r == '"' {
			out = append(out, '\\')
		}
		out = append(out, string(r)...)
	}
	return string(append(out, '"'))
}

func write(t *testing.T, path, content string) {
	t.Helper()
	mkdirs(t, filepath.Dir(path))
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func types(cands []Candidate) map[string]Candidate {
	out := map[string]Candidate{}
	for _, c := range cands {
		out[c.Type] = c
	}
	return out
}

// Synthetic folders of each kind, and look-alikes that must not be taken for bbport.
func TestClassifyNeedsEvidenceForBBPort(t *testing.T) {
	root := t.TempDir()

	// bbport installed: bbport.ini in the port folder, user/ with the log and the patch file.
	port := filepath.Join(root, "bbport")
	write(t, filepath.Join(port, "bbport.ini"), "[video]\nupscaler=fsr\n")
	write(t, filepath.Join(port, "user", "last_run.log"), "starting\n")
	write(t, filepath.Join(port, "user", "trophies.jsonl"), "")
	got := types(Classify(filepath.Join(port, "user")))
	if !got[source.BBPortLog].Certain || !got[source.BBPort].Certain || len(got) != 2 {
		t.Fatalf("bbport user folder with bbport.ini next to it: %+v", got)
	}
	got = types(Classify(port))
	if c, ok := got[source.BBPortLog]; !ok || c.Path != filepath.Join(port, "user") {
		t.Fatalf("the port folder points at its user folder: %+v", got)
	}

	// Only a log in bbport's own format: offered, but not certain.
	logOnly := filepath.Join(root, "log-only")
	write(t, filepath.Join(logOnly, "last_run.log"), "[bbport] v1.2\nRuntime: trophy 3 unlocked\n")
	if c := types(Classify(logOnly))[source.BBPortLog]; c.Type == "" || c.Certain {
		t.Fatalf("log only: %+v", c)
	}

	// The patch's trophies.jsonl, recognised by its format.
	patch := filepath.Join(root, "patched")
	write(t, filepath.Join(patch, "trophies.jsonl"), `{"v":1,"title_id":"CUSA00001","trophy_id":4,"unlocked_at":"2026-10-10T06:30:00Z"}`+"\n")
	if c := types(Classify(patch))[source.BBPort]; c.Type == "" || c.Certain {
		t.Fatalf("patch file: %+v", c)
	}

	// A game dump (or another emulator's folder) with a "last_run.log" and a "trophies.jsonl" that
	// bbport did not write: not bbport.
	dump := filepath.Join(root, "Bloodborne DUMP")
	mkdirs(t, filepath.Join(dump, "sce_sys"))
	write(t, filepath.Join(dump, "last_run.log"), "shadPS4 v0.9 started\nloading eboot\n")
	write(t, filepath.Join(dump, "trophies.jsonl"), "{\"something\":\"else\"}\n")
	if got := Classify(dump); len(got) != 0 {
		t.Fatalf("a dump must not look like bbport: %+v", got)
	}

	// shadPS4 next to a dump: shadPS4, certain, never bbport.
	shad := filepath.Join(root, "shadPS4")
	mkdirs(t, filepath.Join(shad, "user", "home"), filepath.Join(shad, "user", "sys_modules"))
	write(t, filepath.Join(shad, "user", "last_run.log"), "shadPS4 log\n")
	got = types(Classify(shad))
	if len(got) != 1 || !got[source.ShadPS4].Certain {
		t.Fatalf("shadPS4: %+v", got)
	}

	// RPCS3: its folder layout is unique.
	rpcs3 := filepath.Join(root, "rpcs3")
	mkdirs(t, filepath.Join(rpcs3, "dev_hdd0", "home", "00000001"))
	if c := types(Classify(rpcs3))[source.RPCS3]; !c.Certain {
		t.Fatalf("rpcs3: %+v", c)
	}
}

func TestPortableBBPortSharedLayoutBeforeAndAfterFirstPlay(t *testing.T) {
	home := t.TempDir()
	port := filepath.Join(home, "Bloodborne-Windows-v1.7.2", "bbport-windows")
	user := filepath.Join(port, "user")
	mkdirs(t, filepath.Join(user, "home"), filepath.Join(user, "sys_modules"))
	write(t, filepath.Join(port, "bbport.ini"), "[video]\n")
	for _, played := range []bool{false, true} {
		if played {
			write(t, filepath.Join(user, "last_run.log"), "Runtime: trophy 21 unlocked\n")
		}
		got := Find(Env{Home: home})
		if len(got) != 1 || got[0].Type != source.BBPortLog || got[0].Path != user || !got[0].Certain {
			t.Fatalf("played=%v: expected the portable port, not shadPS4: %+v", played, got)
		}
		if classified := Classify(port); len(classified) != 1 || classified[0].Type != source.BBPortLog {
			t.Fatalf("shared user layout hid the port: %+v", classified)
		}
	}
}

func TestPortableProbeDoesNotWalkArbitraryNestedFolders(t *testing.T) {
	home := t.TempDir()
	port := filepath.Join(home, "unrelated", "nested", "bbport-windows")
	mkdirs(t, filepath.Join(port, "user"))
	write(t, filepath.Join(port, "bbport.ini"), "[video]\n")
	if got := Find(Env{Home: home}); len(got) != 0 {
		t.Fatalf("unexpected recursive discovery: %+v", got)
	}
}

// The real Windows layout: the portable package unpacked in Downloads, the launcher settings with an
// empty user_dir (the portable default) but a game folder, the shadPS4-like user tree, a session log
// and the runtime's trophies.log. A real shadPS4 user folder next to it is still shadPS4.
func TestPortablePortGetsTheLauncherGameFolder(t *testing.T) {
	home := t.TempDir()
	appData := filepath.Join(home, "AppData", "Roaming")
	game := filepath.Join(home, "Games", "CUSA03173")
	port := filepath.Join(home, "Downloads", "Bloodborne-Windows-v1.7.2", "bbport-windows")
	user := filepath.Join(port, "user")
	mkdirs(t, filepath.Join(game, "sce_sys"), filepath.Join(user, "home"), filepath.Join(user, "sys_modules"),
		filepath.Join(user, "trophy"), filepath.Join(user, "game_data"), filepath.Join(user, "custom_trophy"))
	write(t, filepath.Join(port, "bbport.ini"), "[video]\n")
	write(t, filepath.Join(user, "last_run.log"), "Bloodborne | entry=0xa0\nRuntime: trophy 21 unlocked\n")
	write(t, filepath.Join(user, "trophies.log"), "21 1791935994\n")
	write(t, filepath.Join(appData, "bbport-launcher", "settings.json"),
		`{"game_dir": `+jsonString(game)+`, "user_dir": ""}`)
	shad := filepath.Join(home, "Downloads", "shadPS4", "user")
	mkdirs(t, filepath.Join(shad, "home", "1000", "trophy"), filepath.Join(shad, "sys_modules"))

	got := types(Find(Env{Home: home, AppData: appData}))
	bb, ok := got[source.BBPortLog]
	if !ok || bb.Path != user || !bb.Certain || bb.GameDir != game {
		t.Fatalf("the portable port with the launcher's game folder: %+v", got)
	}
	if sh, ok := got[source.ShadPS4]; !ok || sh.Path != shad {
		t.Fatalf("a real shadPS4 folder stays shadPS4: %+v", got)
	}
	if len(got) != 2 {
		t.Fatalf("nothing else: %+v", got)
	}
}
