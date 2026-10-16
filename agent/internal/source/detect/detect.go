// Package detect suggests source folders. It never walks the disk: it checks a fixed list of
// usual install locations and looks ONE level deep inside a few obvious folders (home, Desktop,
// Downloads, Documents, Program Files...). Nothing is ever written.
package detect

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/vigil-tracker/agent/internal/source"
	"github.com/vigil-tracker/agent/internal/source/bbportlog"
)

// Candidate is a folder that looks like a source.
type Candidate struct {
	Type string
	Path string
	// GameDir: for bbport-log and gow3-log, the game folder that says which trophy set it is (may be
	// empty).
	GameDir string
	// Certain: the folder surely belongs to that program (its own settings point at it, or files
	// only it creates are there). Uncertain candidates are only worth offering when they already
	// hold trophies.
	Certain bool
	// UpdateDir: for gow3-log, the update folder the port's launcher is set to (may be empty).
	UpdateDir string
}

// Env holds the folders detection starts from; FromOS fills it for the current user.
type Env struct {
	Home         string
	AppData      string // Windows %APPDATA% (roaming)
	LocalAppData string // Windows %LOCALAPPDATA%
	ProgramFiles []string
	GameDirs     []string // e.g. C:\Games
	Extra        []string // more folders to look into (one level), e.g. from --in

	XDGConfigHome string   // Linux $XDG_CONFIG_HOME (the port launcher's settings)
	XDGDataHome   string   // Linux $XDG_DATA_HOME (the port AppImage's data folder)
	GOW3DataDir   string   // $GOW3_DATA_DIR, when this user sets it for the God of War III port
	RunningGOW3   []string // folders of a God of War III port running now (Windows)
}

// FromOS reads the current user's folders from the environment.
func FromOS(extra []string) Env {
	home, _ := os.UserHomeDir()
	env := baseEnv()
	env.Extra = extra
	env.RunningGOW3 = runningGOW3Dirs()
	// <drive>:\Games on every fixed drive (one level each): ports are often on another disk.
	env.GameDirs = append(env.GameDirs, gamesFolders(filepath.VolumeName(home))...)
	seen := map[string]bool{}
	for _, k := range []string{"ProgramFiles", "ProgramFiles(x86)", "ProgramW6432"} {
		if v := os.Getenv(k); v != "" && !seen[strings.ToLower(v)] {
			seen[strings.ToLower(v)] = true
			env.ProgramFiles = append(env.ProgramFiles, v)
		}
	}
	return env
}

// FromOSLight is FromOS without the slow parts (no process list, no drive scan): enough to read the
// launchers' settings.
func FromOSLight() Env { return baseEnv() }

// baseEnv is the current user's folders without anything slow (no process list).
func baseEnv() Env {
	home, _ := os.UserHomeDir()
	return Env{Home: home, AppData: os.Getenv("APPDATA"), LocalAppData: os.Getenv("LOCALAPPDATA"),
		XDGConfigHome: os.Getenv("XDG_CONFIG_HOME"), XDGDataHome: os.Getenv("XDG_DATA_HOME"),
		GOW3DataDir: os.Getenv("GOW3_DATA_DIR")}
}

// maxEntries bounds how many subfolders of one folder are looked at.
const maxEntries = 500

// Find returns the candidates, deduplicated and sorted by type then path.
func Find(env Env) []Candidate {
	found := map[[2]string]Candidate{}
	addCandidate := func(n Candidate) {
		abs, err := filepath.Abs(n.Path)
		if err != nil {
			return
		}
		c := found[[2]string{n.Type, abs}]
		c.Type, c.Path = n.Type, abs
		if n.GameDir != "" {
			c.GameDir = n.GameDir
		}
		if n.UpdateDir != "" {
			c.UpdateDir = n.UpdateDir
		}
		c.Certain = c.Certain || n.Certain
		found[[2]string{n.Type, abs}] = c
	}
	add := func(typ, path, gameDir string, certain bool) {
		addCandidate(Candidate{Type: typ, Path: path, GameDir: gameDir, Certain: certain})
	}
	gow3, near := findGOW3(env, addCandidate)
	check := func(dir string) {
		gow3.check(dir)
		for _, c := range classify(dir, gow3.settings) {
			if c.Type == source.GOW3Log {
				continue // gow3.check added it, with its game folder
			}
			add(c.Type, c.Path, "", c.Certain)
		}
		// Official portable archives wrap the executable directory once. Inspect only
		// these exact package names, never recursively walk an arbitrary game folder.
		for _, name := range []string{"bbport-windows", "bbport-linux"} {
			for _, c := range Classify(filepath.Join(dir, name)) {
				add(c.Type, c.Path, "", c.Certain)
			}
		}
	}

	// Usual install / data locations.
	var fixed []string
	for _, pf := range env.ProgramFiles {
		fixed = append(fixed, filepath.Join(pf, "RPCS3"), filepath.Join(pf, "shadPS4"))
	}
	if env.AppData != "" {
		fixed = append(fixed, filepath.Join(env.AppData, "rpcs3"), filepath.Join(env.AppData, "shadPS4"))
	}
	if env.LocalAppData != "" {
		fixed = append(fixed, filepath.Join(env.LocalAppData, "rpcs3"))
	}
	if env.Home != "" {
		fixed = append(fixed,
			filepath.Join(env.Home, ".config", "rpcs3"),
			filepath.Join(env.Home, ".local", "share", "shadPS4"),
			filepath.Join(env.Home, "Library", "Application Support", "rpcs3"),
			filepath.Join(env.Home, "Library", "Application Support", "shadPS4"))
	}
	for _, p := range fixed {
		check(p)
	}
	// The bbport launcher's settings name its user folder (where last_run.log goes, even before
	// the first run) and the game folder (which says the trophy set). An empty user_dir means the
	// portable default, the "user" folder next to the port, which the folders below find.
	l := bbportLauncher(env.AppData)
	launcherGame := ""
	if l.GameDir != "" && isDir(l.GameDir) {
		launcherGame, _ = filepath.Abs(l.GameDir)
	}
	if l.UserDir != "" && isDir(l.UserDir) {
		add(source.BBPortLog, l.UserDir, launcherGame, true) // the launcher itself says so
		check(l.UserDir)                                     // trophies.jsonl when the Vigil patch is in use
	}

	// One level inside obvious folders.
	var browse []string
	if env.Home != "" {
		browse = append(browse, env.Home,
			filepath.Join(env.Home, "Desktop"), filepath.Join(env.Home, "OneDrive", "Desktop"),
			filepath.Join(env.Home, "Downloads"), filepath.Join(env.Home, "Documents"),
			filepath.Join(env.Home, "OneDrive", "Documents"), filepath.Join(env.Home, "Games"))
	}
	browse = append(browse, env.ProgramFiles...)
	browse = append(browse, env.GameDirs...)
	for _, dir := range near {
		check(dir)
	}
	browse = append(browse, near...)
	for _, dir := range env.Extra {
		check(dir)
	}
	browse = append(browse, env.Extra...)
	for _, dir := range browse {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for i, e := range entries {
			if i >= maxEntries {
				break
			}
			if e.IsDir() {
				check(filepath.Join(dir, e.Name()))
			}
		}
	}

	// A God of War III saves folder holds "Runtime: trophy" lines too: it is the port's, never bbport's.
	for key, c := range found {
		if c.Type == source.BBPortLog && found[[2]string{source.GOW3Log, c.Path}].Type == source.GOW3Log {
			delete(found, key)
		}
	}
	out := make([]Candidate, 0, len(found))
	for _, c := range found {
		// There is one launcher per user: its game folder says the trophy set of every bbport
		// log found, so a portable install reports trophies exactly like a configured one.
		if c.Type == source.BBPortLog && c.GameDir == "" {
			c.GameDir = launcherGame
		}
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Type != out[j].Type {
			return out[i].Type < out[j].Type
		}
		return out[i].Path < out[j].Path
	})
	return out
}

// Classify says which sources dir looks like, from its layout and a few telltale files. A bbport
// folder must show something only bbport writes (bbport.ini next to it or one level up, a
// last_run.log in bbport's format, or a trophies.jsonl in the Vigil patch's format); a game dump
// or another emulator's folder with similarly named files is not enough.
func Classify(dir string) []Candidate {
	return classify(dir, ReadGOW3Settings(baseEnv()))
}

func classify(dir string, gow3 GOW3Settings) []Candidate {
	// The God of War III port first: its saves folder also holds "Runtime: trophy" lines, and its
	// package shares nothing with bbport, so the folder is the port's and never bbport's.
	var out []Candidate
	finder := gow3Finder{settings: gow3, add: func(c Candidate) { out = append(out, c) }}
	switch {
	case IsGOW3Package(dir):
		finder.port(dir)
		if gow3.UserDir != "" {
			finder.userDir(gow3.UserDir, dir)
		}
		return out
	case isGOW3UserDir(dir, gow3):
		finder.userDir(dir, filepath.Dir(dir))
		return out
	}
	for _, d := range []string{dir, filepath.Join(dir, "user")} {
		ini := hasBBPortIni(d)
		if f := filepath.Join(d, "trophies.jsonl"); isFile(f) && (ini || patchJSONL(f)) {
			out = append(out, Candidate{Type: source.BBPort, Path: d, Certain: ini})
		}
		// The launcher creates last_run.log on first play. Detect its existing user
		// directory before that first unlock instead of requiring an old log.
		if f := filepath.Join(d, "last_run.log"); (isFile(f) && (ini || bbportLog(f))) || trophyJournal(filepath.Join(d, bbportlog.TrophyLogName), ini) || (ini && isDir(d) && filepath.Base(d) == "user") {
			out = append(out, Candidate{Type: source.BBPortLog, Path: d, Certain: ini})
		}
		if len(out) > 0 && d == dir {
			break // the folder itself matched: do not also offer its "user" subfolder
		}
	}
	// bbport shares shadPS4's user/home and sys_modules layout. Its own ini/log
	// is stronger evidence; do not mislabel that same directory as an emulator.
	if len(out) == 0 {
		switch {
		case isDir(filepath.Join(dir, "dev_hdd0", "home")):
			out = append(out, Candidate{Type: source.RPCS3, Path: dir, Certain: true})
		case looksLikeShadUser(dir):
			out = append(out, Candidate{Type: source.ShadPS4, Path: dir, Certain: true})
		case looksLikeShadUser(filepath.Join(dir, "user")):
			out = append(out, Candidate{Type: source.ShadPS4, Path: filepath.Join(dir, "user"), Certain: true})
		}
	}
	return out
}

// A numeric journal alone is not a program identity: require bbport's ini too.
func trophyJournal(path string, ini bool) bool {
	if !ini || !isFile(path) {
		return false
	}
	for _, line := range strings.Split(head(path, 4<<10), "\n") {
		if _, _, ok := bbportlog.ParseTrophyLine(line); ok {
			return true
		}
	}
	return false
}

// hasBBPortIni: bbport keeps bbport.ini in its own folder, the parent of "user".
func hasBBPortIni(dir string) bool {
	return isFile(filepath.Join(dir, "bbport.ini")) || isFile(filepath.Join(filepath.Dir(dir), "bbport.ini"))
}

// head reads the first bytes of a file (read only).
func head(path string, n int64) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	buf := make([]byte, n)
	read, _ := f.Read(buf)
	return string(buf[:read])
}

// bbportLog: the launcher's log names the port or prints its runtime lines.
func bbportLog(path string) bool {
	h := strings.ToLower(head(path, 64<<10))
	return strings.Contains(h, "bbport") || strings.Contains(h, "runtime: trophy")
}

// patchJSONL: the Vigil patch writes {"v":1,...,"trophy_id":N,...} lines.
func patchJSONL(path string) bool {
	line, _, _ := strings.Cut(head(path, 4<<10), "\n")
	var v struct {
		V        int  `json:"v"`
		TrophyID *int `json:"trophy_id"`
	}
	return json.Unmarshal([]byte(strings.TrimSpace(line)), &v) == nil && v.V == 1 && v.TrophyID != nil
}

// looksLikeShadUser: a shadPS4 user folder has home/ or game_data/ next to shadPS4-only folders.
func looksLikeShadUser(dir string) bool {
	if !isDir(filepath.Join(dir, "home")) && !isDir(filepath.Join(dir, "game_data")) {
		return false
	}
	for _, marker := range []string{"sys_modules", "custom_trophy", "trophy"} {
		if isDir(filepath.Join(dir, marker)) {
			return true
		}
	}
	return false
}

// launcherSettings is the part of the bbport launcher's settings.json the agent reads.
type launcherSettings struct {
	UserDir string `json:"user_dir"`
	GameDir string `json:"game_dir"`
}

// bbportLauncher reads %APPDATA%/bbport-launcher/settings.json, if any (read only).
func bbportLauncher(appData string) launcherSettings {
	var s launcherSettings
	if appData == "" {
		return s
	}
	data, err := source.ReadLimited(filepath.Join(appData, "bbport-launcher", "settings.json"), 64<<10)
	if err != nil || json.Unmarshal(data, &s) != nil {
		return launcherSettings{}
	}
	return s
}

func isDir(p string) bool {
	info, err := os.Stat(p)
	return err == nil && info.IsDir()
}

func isFile(p string) bool {
	info, err := os.Stat(p)
	return err == nil && info.Mode().IsRegular()
}
