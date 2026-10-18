package detect

import (
	"encoding/json"
	"path/filepath"
	"strings"

	"github.com/vigil-tracker/agent/internal/source"
)

// AjncSystem/ShadowOfTheColossus-PC v0.57: the launcher saves game_dir/user_dir
// in APPDATA/sotc-launcher/settings.json. Logs go to user_dir/last_run.log,
// or <port>/user. A picked savedata subtree is normalized back to the data root.
type SOTCSettings struct {
	GameDir string `json:"game_dir"`
	UserDir string `json:"user_dir"`
	Found   bool   `json:"-"`
}

func ReadSOTCSettings(env Env) SOTCSettings {
	root := env.AppData
	if root == "" {
		root = env.Home
	} // matches this Windows launcher's Path.home fallback
	if root == "" {
		return SOTCSettings{}
	}
	data, err := source.ReadLimited(filepath.Join(root, "sotc-launcher", "settings.json"), 256<<10)
	var s SOTCSettings
	if err != nil || json.Unmarshal(data, &s) != nil {
		return SOTCSettings{}
	}
	s.Found = true
	if s.UserDir != "" {
		s.UserDir = SOTCUserRoot(s.UserDir)
	}
	return s
}

func SOTCUserRoot(path string) string {
	path = filepath.Clean(path)
	for p := path; ; p = filepath.Dir(p) {
		if strings.EqualFold(filepath.Base(p), "savedata") {
			return filepath.Dir(p)
		}
		if filepath.Dir(p) == p {
			return path
		}
	}
}

func IsSOTCPackage(dir string) bool {
	// bb-probe alone is shared with Bloodborne: require a Shadow-specific marker.
	return isFile(filepath.Join(dir, "sotc.py")) &&
		(isFile(filepath.Join(dir, "SotC.exe")) || isFile(filepath.Join(dir, "launcher", "sotc_launcher_win.py")))
}

type sotcFinder struct {
	settings SOTCSettings
	add      func(Candidate)
}

func (s sotcFinder) userDir(user, port string) {
	game := s.settings.GameDir
	if game == "" && port != "" {
		p := filepath.Join(filepath.Dir(port), "CUSA08809")
		if isDir(p) {
			game = p
		}
	}
	if game != "" {
		if p, err := filepath.Abs(game); err == nil {
			game = p
		}
	}
	s.add(Candidate{Type: source.SOTCLog, Path: user, GameDir: game, Certain: true})
}

func (s sotcFinder) port(dir string) {
	if !IsSOTCPackage(dir) || s.settings.UserDir != "" {
		return
	}
	s.userDir(filepath.Join(dir, "user"), dir)
}

func (s sotcFinder) check(dir string) {
	s.port(dir)
	s.port(filepath.Join(dir, "SotC-PC"))
}

func findSOTC(env Env, add func(Candidate)) (sotcFinder, []string) {
	s := sotcFinder{settings: ReadSOTCSettings(env), add: add}
	if s.settings.GameDir == "" {
		s.settings.GameDir = env.SOTCGameDir
	}
	if s.settings.UserDir == "" && env.SOTCUserDir != "" {
		s.settings.UserDir = SOTCUserRoot(env.SOTCUserDir)
	}
	if s.settings.UserDir != "" {
		s.userDir(s.settings.UserDir, "")
	}
	near := append([]string{}, env.RunningSOTC...)
	if s.settings.GameDir != "" {
		near = append(near, filepath.Dir(s.settings.GameDir))
	}
	return s, near
}

func classifySOTC(dir string, settings SOTCSettings) []Candidate {
	var out []Candidate
	f := sotcFinder{settings: settings, add: func(c Candidate) { out = append(out, c) }}
	if IsSOTCPackage(dir) {
		if settings.UserDir != "" {
			f.userDir(settings.UserDir, dir)
		} else {
			f.port(dir)
		}
	} else if (settings.UserDir != "" && sameDir(dir, settings.UserDir)) ||
		(strings.EqualFold(filepath.Base(dir), "user") && IsSOTCPackage(filepath.Dir(dir))) {
		f.userDir(dir, filepath.Dir(dir))
	}
	return out
}
