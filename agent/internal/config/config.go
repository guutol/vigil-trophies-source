// Package config manages the agent's local files in the OS config directory:
//
//	config.json   server URL and sources (type + folder)
//	token         bearer token (0600; on Windows protected only by the user profile ACLs)
//	known.json    event ids already queued or sent
//	catalogs.json trophy sets whose catalog was already proposed (or is known)
//	outbox.jsonl  events waiting to be sent
package config

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/vigil-tracker/agent/internal/outbox"
)

// DefaultServer is used when neither --server nor a saved config is present. Release builds can
// point it at production with
// -ldflags "-X github.com/vigil-tracker/agent/internal/config.DefaultServer=https://vigil.example.com".
var DefaultServer = "http://localhost:8080"

// Store is the agent's config directory.
type Store struct {
	Dir        string
	settingsMu sync.Mutex // serialize read/modify/write of config.json within the running app
}

// Open returns the store at $VIGIL_CONFIG_DIR or <UserConfigDir>/vigil, creating it (0700).
func Open() (*Store, error) {
	dir := os.Getenv("VIGIL_CONFIG_DIR")
	if dir == "" {
		base, err := os.UserConfigDir()
		if err != nil {
			return nil, err
		}
		dir = filepath.Join(base, "vigil")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	return &Store{Dir: dir}, nil
}

func (s *Store) path(name string) string { return filepath.Join(s.Dir, name) }

// OutboxPath is where the pending events live.
func (s *Store) OutboxPath() string { return s.path("outbox.jsonl") }

// ---- config.json: server and sources ----

// SourceConfig is one configured source: its type (bbport, bbport-log, rpcs3, shadps4) and
// folder. GameDir is used by bbport-log, gow3-log and sotc-log: the game folder that says which trophy set it
// is. UpdateDir is used by gow3-log: the update folder the port's launcher is set to.
type SourceConfig struct {
	Type      string `json:"type"`
	Path      string `json:"path"`
	GameDir   string `json:"game_dir,omitempty"`
	UpdateDir string `json:"update_dir,omitempty"`
}

type settings struct {
	Server  string         `json:"server"`
	Sources []SourceConfig `json:"sources,omitempty"`
	// CatalogProposals: send catalogs of unknown trophy sets to the server (default on).
	CatalogProposals *bool `json:"catalog_proposals,omitempty"`
	// TraySetupDone: the tray app already offered the detected sources once.
	TraySetupDone bool `json:"tray_setup_done,omitempty"`
	// DetectVersion: the source detection the tray app last offered (0 before it was recorded).
	DetectVersion int `json:"detect_version,omitempty"`
	// Notifications: how the tray app announces trophies.
	Notifications *NotifyPrefs `json:"notifications,omitempty"`
	// Language of the tray app: auto (follow Windows), en or pt-BR.
	Language string `json:"language,omitempty"`
	// Stopped: folders the player stopped watching (stopped.go); detection never offers them again.
	Stopped    []StoppedFolder `json:"stopped,omitempty"`
	SystemMenu bool            `json:"system_menu,omitempty"`
}

func (s *Store) SystemMenu() bool { return s.settings().SystemMenu }
func (s *Store) SetSystemMenu(on bool) error {
	s.settingsMu.Lock()
	defer s.settingsMu.Unlock()
	cfg := s.settings()
	cfg.SystemMenu = on
	return writeJSON(s.path("config.json"), cfg)
}

// Language is the saved language choice: "auto" (the default), "en" or "pt-BR".
func (s *Store) Language() string {
	switch l := s.settings().Language; l {
	case "en", "pt-BR":
		return l
	}
	return "auto"
}

// SetLanguage saves the language choice (anything unknown becomes "auto").
func (s *Store) SetLanguage(lang string) error {
	s.settingsMu.Lock()
	defer s.settingsMu.Unlock()
	cfg := s.settings()
	switch lang {
	case "en", "pt-BR":
		cfg.Language = lang
	default:
		cfg.Language = ""
	}
	return writeJSON(s.path("config.json"), cfg)
}

// Notification styles and corners.
const (
	StyleVigil   = "vigil"   // the agent's own popup
	StyleWindows = "windows" // a Windows notification from the tray icon
	StyleOff     = "off"

	CornerTopRight    = "top-right"
	CornerTopLeft     = "top-left"
	CornerBottomRight = "bottom-right"
	CornerBottomLeft  = "bottom-left"

	VolumeOff    = "off"
	VolumeLow    = "low"
	VolumeMedium = "medium"
	VolumeHigh   = "high"
)

// Volumes lists the sound levels, quietest first.
var Volumes = []string{VolumeOff, VolumeLow, VolumeMedium, VolumeHigh}

// Corners lists the popup positions.
var Corners = []string{CornerTopRight, CornerTopLeft, CornerBottomRight, CornerBottomLeft}

// NotifyPrefs are the notification preferences. Missing or unknown values fall back to the
// defaults: Vigil style, low volume, top-right corner.
type NotifyPrefs struct {
	Style  string `json:"style,omitempty"`
	Volume string `json:"volume,omitempty"`
	// Sound is the older on/off setting; "off" there becomes VolumeOff.
	Sound  *bool  `json:"sound,omitempty"`
	Corner string `json:"corner,omitempty"`
}

// SoundOn is true unless the volume is off.
func (p NotifyPrefs) SoundOn() bool { return p.Normalized().Volume != VolumeOff }

// Normalized fills defaults and drops unknown values.
func (p NotifyPrefs) Normalized() NotifyPrefs {
	switch p.Style {
	case StyleVigil, StyleWindows, StyleOff:
	default:
		p.Style = StyleVigil
	}
	valid := false
	for _, c := range Corners {
		valid = valid || p.Corner == c
	}
	if !valid {
		p.Corner = CornerTopRight
	}
	switch p.Volume {
	case VolumeOff, VolumeLow, VolumeMedium, VolumeHigh:
	default:
		p.Volume = VolumeLow
		if p.Sound != nil && !*p.Sound {
			p.Volume = VolumeOff
		}
	}
	p.Sound = nil
	return p
}

// NotifyPrefs returns the saved preferences, normalized.
func (s *Store) NotifyPrefs() NotifyPrefs {
	if p := s.settings().Notifications; p != nil {
		return p.Normalized()
	}
	return NotifyPrefs{}.Normalized()
}

// SetNotifyPrefs saves the preferences (normalized).
func (s *Store) SetNotifyPrefs(p NotifyPrefs) error {
	s.settingsMu.Lock()
	defer s.settingsMu.Unlock()
	cfg := s.settings()
	n := p.Normalized()
	cfg.Notifications = &n
	return writeJSON(s.path("config.json"), cfg)
}

// TraySetupDone is true after the tray app's first-run source detection.
func (s *Store) TraySetupDone() bool { return s.settings().TraySetupDone }

// SetTraySetupDone records that the first-run detection was offered.
func (s *Store) SetTraySetupDone() error {
	s.settingsMu.Lock()
	defer s.settingsMu.Unlock()
	cfg := s.settings()
	cfg.TraySetupDone = true
	return writeJSON(s.path("config.json"), cfg)
}

// DetectVersion is the source detection version the tray app last offered.
func (s *Store) DetectVersion() int { return s.settings().DetectVersion }

// SetDetectVersion records that the sources of detection version v were offered.
func (s *Store) SetDetectVersion(v int) error {
	s.settingsMu.Lock()
	defer s.settingsMu.Unlock()
	cfg := s.settings()
	cfg.DetectVersion = v
	return writeJSON(s.path("config.json"), cfg)
}

// CatalogProposalsEnabled is on unless the user turned it off.
func (s *Store) CatalogProposalsEnabled() bool {
	v := s.settings().CatalogProposals
	return v == nil || *v
}

// SetCatalogProposals turns catalog proposals on or off.
func (s *Store) SetCatalogProposals(on bool) error {
	s.settingsMu.Lock()
	defer s.settingsMu.Unlock()
	cfg := s.settings()
	cfg.CatalogProposals = &on
	return writeJSON(s.path("config.json"), cfg)
}

// HandledCatalogs are the trophy sets already proposed, known by the server or refused.
func (s *Store) HandledCatalogs() map[string]bool {
	var ids []string
	_ = readJSON(s.path("catalogs.json"), &ids)
	out := make(map[string]bool, len(ids))
	for _, id := range ids {
		out[id] = true
	}
	return out
}

// SetHandledCatalogs saves them, sorted.
func (s *Store) SetHandledCatalogs(handled map[string]bool) error {
	ids := make([]string, 0, len(handled))
	for id := range handled {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return writeJSON(s.path("catalogs.json"), ids)
}

func (s *Store) settings() settings {
	var cfg settings
	_ = readJSON(s.path("config.json"), &cfg)
	return cfg
}

// Server returns the saved server URL, or DefaultServer.
func (s *Store) Server() string {
	if cfg := s.settings(); cfg.Server != "" {
		return cfg.Server
	}
	return DefaultServer
}

// SetServer saves the server URL, keeping the sources.
func (s *Store) SetServer(server string) error {
	s.settingsMu.Lock()
	defer s.settingsMu.Unlock()
	cfg := s.settings()
	cfg.Server = server
	return writeJSON(s.path("config.json"), cfg)
}

// Sources returns the configured sources.
func (s *Store) Sources() []SourceConfig {
	return s.settings().Sources
}

// AddSource saves a source; adding the same type and path twice is a no-op (returns false).
// Adding a source is an explicit choice, so its folder also leaves the stopped list (StopWatching).
func (s *Store) AddSource(src SourceConfig) (bool, error) {
	s.settingsMu.Lock()
	defer s.settingsMu.Unlock()
	cfg := s.settings()
	stopped := withoutStopped(cfg.Stopped, src.Path)
	changed := len(stopped) != len(cfg.Stopped)
	cfg.Stopped = stopped
	added := !hasSource(cfg.Sources, src)
	if added {
		cfg.Sources = append(cfg.Sources, src)
	}
	if !added && !changed {
		return false, nil
	}
	return added, writeJSON(s.path("config.json"), cfg)
}

// RemoveSource deletes the sources with that type and path; returns how many were removed.
func (s *Store) RemoveSource(src SourceConfig) (int, error) {
	s.settingsMu.Lock()
	defer s.settingsMu.Unlock()
	cfg := s.settings()
	kept := cfg.Sources[:0]
	for _, existing := range cfg.Sources {
		if existing.Type == src.Type && strings.EqualFold(filepath.Clean(existing.Path), filepath.Clean(src.Path)) {
			continue
		}
		kept = append(kept, existing)
	}
	removed := len(cfg.Sources) - len(kept)
	cfg.Sources = kept
	if removed == 0 {
		return 0, nil
	}
	return removed, writeJSON(s.path("config.json"), cfg)
}

// ---- token ----

// Token returns the saved token, or "" if the agent is not linked.
func (s *Store) Token() (string, error) {
	data, err := os.ReadFile(s.path("token"))
	if errors.Is(err, fs.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(data)), nil
}

// SetToken saves the token with mode 0600.
func (s *Store) SetToken(token string) error {
	return outbox.WriteFileAtomic(s.path("token"), []byte(token+"\n"), 0o600)
}

// DeleteToken removes the token. Removing a missing token is not an error.
func (s *Store) DeleteToken() error {
	err := os.Remove(s.path("token"))
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return err
}

// ---- known event ids ----

// Known returns the event ids already queued or sent, so re-reading a file does not queue them
// again. A missing or corrupt file gives an empty set (the server deduplicates anyway).
func (s *Store) Known() map[string]bool {
	var ids []string
	_ = readJSON(s.path("known.json"), &ids)
	out := make(map[string]bool, len(ids))
	for _, id := range ids {
		out[id] = true
	}
	return out
}

// SetKnown saves the known ids atomically, sorted so the file is stable.
func (s *Store) SetKnown(known map[string]bool) error {
	ids := make([]string, 0, len(known))
	for id := range known {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return writeJSON(s.path("known.json"), ids)
}

func readJSON(path string, v any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, v)
}

func writeJSON(path string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return outbox.WriteFileAtomic(path, append(data, '\n'), 0o600)
}

// UpdatePath is where the update check keeps its last answer (internal/update.FileState).
func (s *Store) UpdatePath() string { return s.path("update.json") }
