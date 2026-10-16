package config

import (
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// StoppedFolder is a folder the player deliberately stopped watching ("Stop watching"). Detection
// never offers it again and nothing adds it back by itself; only an explicit action does (the
// "Watch again" entry, adding the folder by hand, or "sources add"). Sources keeps what was
// configured there, so watching again restores it as it was. "Not now" in the detection dialog is
// not a removal and is not recorded here.
type StoppedFolder struct {
	Path    string         `json:"path"`
	Sources []SourceConfig `json:"sources,omitempty"`
	At      time.Time      `json:"at"`
}

// FolderKey compares folders the way Windows does: absolute, cleaned (one kind of separator, no
// trailing separator, no "." or ".."), without the \\?\ prefix, and case-insensitive on Windows.
// Elsewhere paths stay case-sensitive.
func FolderKey(path string) string {
	p := path
	if runtime.GOOS == "windows" {
		p = strings.ReplaceAll(p, "/", `\`)
		if strings.HasPrefix(strings.ToLower(p), `\\?\unc\`) {
			p = `\\` + p[8:]
		} else {
			p = strings.TrimPrefix(p, `\\?\`)
		}
	}
	if abs, err := filepath.Abs(p); err == nil {
		p = abs
	}
	p = filepath.Clean(p)
	if runtime.GOOS == "windows" {
		p = strings.ToLower(p)
	}
	return p
}

// SameFolder reports whether a and b are the same folder (FolderKey).
func SameFolder(a, b string) bool { return a != "" && b != "" && FolderKey(a) == FolderKey(b) }

// StoppedFolders are the folders the player stopped watching, oldest first.
func (s *Store) StoppedFolders() []StoppedFolder { return s.settings().Stopped }

// IsStopped reports whether path is a folder the player stopped watching.
func (s *Store) IsStopped(path string) bool {
	for _, f := range s.settings().Stopped {
		if SameFolder(f.Path, path) {
			return true
		}
	}
	return false
}

// StopWatching removes every source of the folder at path from the configuration and records the
// folder as stopped (with what was configured there). Only the configuration changes: the folder,
// its files, the queue and what was already sent stay as they are. removed is how many sources
// were configured in the folder.
func (s *Store) StopWatching(path string, at time.Time) (removed int, err error) {
	s.settingsMu.Lock()
	defer s.settingsMu.Unlock()
	cfg := s.settings()
	stopped := StoppedFolder{Path: path, At: at.UTC()}
	kept := cfg.Sources[:0:0]
	for _, c := range cfg.Sources {
		if SameFolder(c.Path, path) {
			stopped.Sources = append(stopped.Sources, c)
			continue
		}
		kept = append(kept, c)
	}
	cfg.Sources = kept
	list := cfg.Stopped[:0:0]
	for _, f := range cfg.Stopped {
		if SameFolder(f.Path, path) {
			stopped.Sources = appendMissing(stopped.Sources, f.Sources)
			continue
		}
		list = append(list, f)
	}
	cfg.Stopped = append(list, stopped)
	return len(stopped.Sources), writeJSON(s.path("config.json"), cfg)
}

// WatchAgain takes the folder off the stopped list and configures again what was there. It is the
// explicit way back: nothing calls it on its own. restored is how many sources came back.
func (s *Store) WatchAgain(path string) (restored int, err error) {
	s.settingsMu.Lock()
	defer s.settingsMu.Unlock()
	cfg := s.settings()
	list := cfg.Stopped[:0:0]
	var back []SourceConfig
	found := false
	for _, f := range cfg.Stopped {
		if SameFolder(f.Path, path) {
			found = true
			back = appendMissing(back, f.Sources)
			continue
		}
		list = append(list, f)
	}
	if !found {
		return 0, nil
	}
	cfg.Stopped = list
	for _, c := range back {
		if !hasSource(cfg.Sources, c) {
			cfg.Sources = append(cfg.Sources, c)
			restored++
		}
	}
	return restored, writeJSON(s.path("config.json"), cfg)
}

func hasSource(list []SourceConfig, c SourceConfig) bool {
	for _, e := range list {
		if e.Type == c.Type && SameFolder(e.Path, c.Path) {
			return true
		}
	}
	return false
}

func appendMissing(list, more []SourceConfig) []SourceConfig {
	for _, c := range more {
		if !hasSource(list, c) {
			list = append(list, c)
		}
	}
	return list
}

func withoutStopped(list []StoppedFolder, path string) []StoppedFolder {
	out := list[:0:0]
	for _, f := range list {
		if !SameFolder(f.Path, path) {
			out = append(out, f)
		}
	}
	return out
}
