package detect

import (
	"encoding/json"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/vigil-tracker/agent/internal/source"
)

// The God of War III Remastered port (github.com/FelipePrado0/gow3_pc, checked at windows-v2.4.0):
//
//   - its launcher keeps settings in %APPDATA%\gow3-launcher\settings.json (Linux:
//     $XDG_CONFIG_HOME or ~/.config, then gow3-launcher/settings.json): game_dir, update_dir and
//     user_dir (the saves folder);
//   - the game's output goes to <saves folder>/last_run.log, where the saves folder is user_dir, or
//     <data folder>/user when user_dir is empty; the data folder is $GOW3_DATA_DIR, else the port's
//     own folder (the Windows package, a source checkout) and, for the AppImage,
//     $XDG_DATA_HOME/gow3 or ~/.local/share/gow3;
//   - the Windows package (gow3-windows) holds "God of War III.exe", "Play God of War III.exe",
//     bin\gow3-probe.exe and patches\God_of_War_III_Remastered.xml; with no saved setting the
//     launcher looks for the game in CUSA01623 next to the package.
//
// Detection only looks at those places, at the folders the settings name and at the package
// folder of a running port; it never walks a disk.

// GOW3PackageName is the folder the official Windows zip extracts to.
const GOW3PackageName = "gow3-windows"

// GOW3Settings is the part of the port launcher's settings.json the agent reads (read only).
type GOW3Settings struct {
	GameDir   string `json:"game_dir"`
	UpdateDir string `json:"update_dir"`
	UserDir   string `json:"user_dir"`
	Found     bool   `json:"-"`
}

// GOW3SettingsPath is where the port's launcher keeps its settings for this user ("" if unknown).
func GOW3SettingsPath(env Env) string {
	switch {
	case env.AppData != "":
		return filepath.Join(env.AppData, "gow3-launcher", "settings.json")
	case env.XDGConfigHome != "":
		return filepath.Join(env.XDGConfigHome, "gow3-launcher", "settings.json")
	case env.Home != "":
		return filepath.Join(env.Home, ".config", "gow3-launcher", "settings.json")
	}
	return ""
}

// ReadGOW3Settings reads the launcher's settings; an absent or broken file gives Found=false.
func ReadGOW3Settings(env Env) GOW3Settings {
	path := GOW3SettingsPath(env)
	if path == "" {
		return GOW3Settings{}
	}
	data, err := source.ReadLimited(path, 256<<10)
	var s GOW3Settings
	if err != nil || json.Unmarshal(data, &s) != nil {
		return GOW3Settings{}
	}
	s.Found = true
	return s
}

// IsGOW3Package reports whether dir is the port's own folder (the Windows package, a Linux build
// or a source checkout), from files only the port has.
func IsGOW3Package(dir string) bool {
	if dir == "" {
		return false
	}
	for _, probe := range []string{
		filepath.Join(dir, "bin", "gow3-probe.exe"),
		filepath.Join(dir, "bin", "gow3-probe"),
		filepath.Join(dir, "out", "gow3-probe.exe"),
		filepath.Join(dir, "out", "gow3-probe"),
	} {
		if isFile(probe) {
			return true
		}
	}
	patch := isFile(filepath.Join(dir, "patches", "God_of_War_III_Remastered.xml"))
	return patch && (isFile(filepath.Join(dir, "God of War III.exe")) || isFile(filepath.Join(dir, "run.py")))
}

// isGOW3UserDir: a saves folder of the port (its "user" folder next to the package, or the folder
// the launcher's settings name).
func isGOW3UserDir(dir string, settings GOW3Settings) bool {
	if settings.UserDir != "" && sameDir(dir, settings.UserDir) {
		return true
	}
	return strings.EqualFold(filepath.Base(dir), "user") && IsGOW3Package(filepath.Dir(dir))
}

func sameDir(a, b string) bool {
	x, err1 := filepath.Abs(a)
	y, err2 := filepath.Abs(b)
	if err1 != nil || err2 != nil {
		return false
	}
	if runtime.GOOS == "windows" {
		return strings.EqualFold(filepath.Clean(x), filepath.Clean(y))
	}
	return filepath.Clean(x) == filepath.Clean(y)
}

// gow3Finder collects the port's saves folders and the game folder each one reports for.
type gow3Finder struct {
	settings GOW3Settings
	add      func(c Candidate)
}

// userDir adds a saves folder of the port, with the game and update folders it reports for.
func (g gow3Finder) userDir(userDir, portDir string) {
	gameDir, updateDir := g.settings.GameDir, g.settings.UpdateDir
	if gameDir == "" && portDir != "" {
		// The launcher's own default with no saved setting: CUSA01623 next to the package.
		if def := filepath.Join(filepath.Dir(portDir), "CUSA01623"); isDir(def) {
			gameDir = def
		}
	}
	if gameDir != "" {
		if abs, err := filepath.Abs(gameDir); err == nil {
			gameDir = abs
		}
	}
	if updateDir != "" {
		if abs, err := filepath.Abs(updateDir); err == nil {
			updateDir = abs
		}
	}
	g.add(Candidate{Type: source.GOW3Log, Path: userDir, GameDir: gameDir, UpdateDir: updateDir, Certain: true})
}

// port adds the saves folder of a port folder: <port>/user, or the launcher's user_dir.
func (g gow3Finder) port(portDir string) {
	if !IsGOW3Package(portDir) {
		return
	}
	abs, err := filepath.Abs(portDir)
	if err != nil {
		return
	}
	if g.settings.UserDir != "" {
		return // the launcher writes the log to user_dir, wherever the package is (added once below)
	}
	g.userDir(filepath.Join(abs, "user"), abs)
}

// findGOW3 adds the port's saves folders: the launcher's settings, $GOW3_DATA_DIR, the AppImage's
// data folder, the package next to the configured game folder, packages one level inside the
// usual folders (done by Find through check) and the folder of a running port.
func findGOW3(env Env, add func(Candidate)) (gow3Finder, []string) {
	g := gow3Finder{settings: ReadGOW3Settings(env), add: add}
	if g.settings.UserDir != "" {
		if abs, err := filepath.Abs(g.settings.UserDir); err == nil {
			g.userDir(abs, "")
		}
	}
	if env.GOW3DataDir != "" && g.settings.UserDir == "" {
		g.userDir(filepath.Join(env.GOW3DataDir, "user"), "")
	}
	// AppImage: AppRun sets the data folder to $XDG_DATA_HOME/gow3 (default ~/.local/share/gow3).
	if runtime.GOOS != "windows" && g.settings.UserDir == "" && env.GOW3DataDir == "" {
		data := env.XDGDataHome
		if data == "" && env.Home != "" {
			data = filepath.Join(env.Home, ".local", "share")
		}
		if data != "" && isDir(filepath.Join(data, "gow3")) {
			g.userDir(filepath.Join(data, "gow3", "user"), "")
		}
	}
	// Places worth one look: next to the configured game folder (the launcher's default layout puts
	// the package beside CUSA01623, on whatever disk), and the folder of a running port.
	var near []string
	if g.settings.GameDir != "" {
		near = append(near, filepath.Dir(g.settings.GameDir))
	}
	near = append(near, env.RunningGOW3...)
	return g, near
}

// check looks at dir as a port folder, and at the official package folder inside it.
func (g gow3Finder) check(dir string) {
	g.port(dir)
	g.port(filepath.Join(dir, GOW3PackageName))
}
