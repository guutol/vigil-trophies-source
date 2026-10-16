package config

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestStoreRoundTrip(t *testing.T) {
	t.Setenv("VIGIL_CONFIG_DIR", t.TempDir())
	s, err := Open()
	if err != nil {
		t.Fatal(err)
	}
	if s.Server() != DefaultServer {
		t.Fatalf("default server: %s", s.Server())
	}
	if tok, _ := s.Token(); tok != "" {
		t.Fatal("no token expected")
	}
	if err := s.SetServer("https://vigil.example"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetToken("vgl_abc"); err != nil {
		t.Fatal(err)
	}
	if tok, _ := s.Token(); tok != "vgl_abc" || s.Server() != "https://vigil.example" {
		t.Fatalf("round trip failed: %q %q", tok, s.Server())
	}
	if runtime.GOOS != "windows" {
		info, _ := os.Stat(filepath.Join(s.Dir, "token"))
		if info.Mode().Perm() != 0o600 {
			t.Fatalf("token mode %v", info.Mode().Perm())
		}
	}
	known := map[string]bool{"b": true, "a": true}
	if err := s.SetKnown(known); err != nil || len(s.Known()) != 2 || !s.Known()["a"] {
		t.Fatalf("known round trip: %v %v", err, s.Known())
	}
	if err := s.DeleteToken(); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteToken(); err != nil {
		t.Fatal("second delete should be a no-op")
	}
	if tok, _ := s.Token(); tok != "" {
		t.Fatal("token should be gone")
	}
}

func TestSourcesKeepTheServer(t *testing.T) {
	t.Setenv("VIGIL_CONFIG_DIR", t.TempDir())
	s, _ := Open()
	s.SetServer("https://vigil.example")
	added, err := s.AddSource(SourceConfig{Type: "rpcs3", Path: `C:\RPCS3`})
	if err != nil || !added {
		t.Fatal("add failed")
	}
	if again, _ := s.AddSource(SourceConfig{Type: "rpcs3", Path: `c:\rpcs3\`}); again && runtime.GOOS == "windows" {
		t.Fatal("same folder twice")
	}
	s.AddSource(SourceConfig{Type: "shadps4", Path: "/x/user"})
	if s.Server() != "https://vigil.example" || len(s.Sources()) < 2 {
		t.Fatalf("server %q sources %v", s.Server(), s.Sources())
	}
	if n, _ := s.RemoveSource(SourceConfig{Type: "shadps4", Path: "/x/user"}); n != 1 {
		t.Fatal("remove")
	}
	if s.Sources()[0].Type != "rpcs3" {
		t.Fatal("wrong source removed")
	}
}

func TestNotifyPrefsDefaultsAndRoundTrip(t *testing.T) {
	t.Setenv("VIGIL_CONFIG_DIR", t.TempDir())
	s, err := Open()
	if err != nil {
		t.Fatal(err)
	}
	p := s.NotifyPrefs()
	if p.Style != StyleVigil || !p.SoundOn() || p.Volume != VolumeLow || p.Corner != CornerTopRight {
		t.Fatalf("defaults: %+v", p)
	}
	if err := s.SetNotifyPrefs(NotifyPrefs{Style: StyleWindows, Volume: VolumeOff, Corner: CornerBottomLeft}); err != nil {
		t.Fatal(err)
	}
	p = s.NotifyPrefs()
	if p.Style != StyleWindows || p.SoundOn() || p.Corner != CornerBottomLeft {
		t.Fatalf("saved: %+v", p)
	}
	// Other settings survive.
	s.SetServer("https://v.example")
	if s.NotifyPrefs().Style != StyleWindows || s.Server() != "https://v.example" {
		t.Fatal("settings overwrite each other")
	}
	// Garbage in the file falls back to the defaults.
	s.SetNotifyPrefs(NotifyPrefs{Style: "flashy", Corner: "middle"})
	if p := s.NotifyPrefs(); p.Style != StyleVigil || p.Corner != CornerTopRight {
		t.Fatalf("unknown values: %+v", p)
	}
}

func TestOldSoundSettingAndVolumes(t *testing.T) {
	off, on := false, true
	if v := (NotifyPrefs{Sound: &off}).Normalized().Volume; v != VolumeOff {
		t.Fatalf("old sound off: %s", v)
	}
	if v := (NotifyPrefs{Sound: &on}).Normalized().Volume; v != VolumeLow {
		t.Fatalf("old sound on: %s", v)
	}
	if v := (NotifyPrefs{Volume: VolumeHigh, Sound: &off}).Normalized().Volume; v != VolumeHigh {
		t.Fatal("the new setting wins")
	}
	if v := (NotifyPrefs{Volume: "deafening"}).Normalized().Volume; v != VolumeLow {
		t.Fatal("unknown volume")
	}
}

func TestLanguagePreference(t *testing.T) {
	t.Setenv("VIGIL_CONFIG_DIR", t.TempDir())
	s, _ := Open()
	if s.Language() != "auto" {
		t.Fatal("default is auto")
	}
	s.SetLanguage("pt-BR")
	if s.Language() != "pt-BR" {
		t.Fatal("saved")
	}
	s.SetLanguage("fr")
	if s.Language() != "auto" {
		t.Fatal("unknown becomes auto")
	}
}
