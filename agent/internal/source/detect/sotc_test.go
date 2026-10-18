package detect

import (
	"path/filepath"
	"testing"

	"github.com/vigil-tracker/agent/internal/source"
)

func sotcPackage(t *testing.T, dir string) string {
	t.Helper()
	for _, f := range []string{"SotC.exe", "sotc.py", filepath.Join("bin", "bb-probe.exe")} {
		write(t, filepath.Join(dir, f), "synthetic")
	}
	return dir
}

func TestSOTCPackageBeforeFirstRun(t *testing.T) {
	home := t.TempDir()
	pkg := sotcPackage(t, filepath.Join(home, "Downloads", "SotC-PC"))
	game := filepath.Join(home, "Downloads", "CUSA08809")
	mkdirs(t, game)
	got := Find(Env{Home: home})
	if len(got) != 1 || got[0].Type != source.SOTCLog || got[0].Path != filepath.Join(pkg, "user") || got[0].GameDir != game {
		t.Fatalf("discovery: %+v", got)
	}
}

func TestSOTCSettingsAndNestedSavedata(t *testing.T) {
	app, data, game := t.TempDir(), t.TempDir(), t.TempDir()
	write(t, filepath.Join(app, "sotc-launcher", "settings.json"), settingsJSON(game, "", filepath.Join(data, "savedata", "1", "CUSA08809")))
	write(t, filepath.Join(data, "last_run.log"), "Runtime: trophy 1 unlocked\n")
	got := Find(Env{AppData: app, Extra: []string{data}})
	if len(got) != 1 || got[0].Type != source.SOTCLog || got[0].Path != data || got[0].GameDir != game {
		t.Fatalf("custom data root: %+v", got)
	}
}

func TestSOTCRunningPortableOutsideUsualFolders(t *testing.T) {
	pkg := sotcPackage(t, filepath.Join(t.TempDir(), "renamed-port"))
	got := Find(Env{RunningSOTC: []string{pkg}})
	if len(got) != 1 || got[0].Type != source.SOTCLog || got[0].Path != filepath.Join(pkg, "user") {
		t.Fatalf("running port: %+v", got)
	}
	unrelated := t.TempDir()
	write(t, filepath.Join(unrelated, "bin", "bb-probe.exe"), "shared executable name alone is not identity")
	if IsSOTCPackage(unrelated) {
		t.Fatal("unrelated directory classified as Shadow")
	}
}
