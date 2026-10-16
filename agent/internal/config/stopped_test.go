package config

import (
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"
)

func TestFolderKeyFollowsWindowsRules(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows path rules")
	}
	same := []string{
		`C:\Games\BB\user`,
		`c:\games\bb\USER`,
		`C:/Games/BB/user/`,
		`C:\Games\BB\user\.`,
		`C:\Games\BB\x\..\user\`,
		`\\?\C:\Games\BB\user`,
	}
	for _, p := range same {
		if !SameFolder(same[0], p) {
			t.Errorf("%q should be the same folder as %q", p, same[0])
		}
	}
	if SameFolder(`C:\Games\BB\user`, `C:\Games\BB\user2`) || SameFolder(`C:\Games\BB\user`, "") {
		t.Error("different folders compared equal")
	}
}

func TestFolderChangesKeepConcurrentPreferences(t *testing.T) {
	s := testStore(t)
	a, b := t.TempDir(), t.TempDir()
	s.AddSource(SourceConfig{Type: "bbport-log", Path: a})
	s.AddSource(SourceConfig{Type: "rpcs3", Path: b})
	var wg sync.WaitGroup
	for _, job := range []func() error{
		func() error { _, err := s.StopWatching(a, time.Now()); return err },
		func() error { _, err := s.StopWatching(b, time.Now()); return err },
		func() error { return s.SetLanguage("pt-BR") },
		func() error { return s.SetSystemMenu(true) },
	} {
		wg.Add(1)
		go func(f func() error) {
			defer wg.Done()
			if err := f(); err != nil {
				t.Error(err)
			}
		}(job)
	}
	wg.Wait()
	if len(s.Sources()) != 0 || len(s.StoppedFolders()) != 2 || s.Language() != "pt-BR" || !s.SystemMenu() {
		t.Fatal("concurrent settings update lost a folder or preference")
	}
	if !(&Store{Dir: s.Dir}).SystemMenu() {
		t.Fatal("system menu preference did not persist")
	}
}

func testStore(t *testing.T) *Store {
	t.Helper()
	return &Store{Dir: t.TempDir()}
}

func TestStopWatchingOneFolderAmongSeveral(t *testing.T) {
	s := testStore(t)
	bb := filepath.Join(t.TempDir(), "bbport", "user")
	rp := filepath.Join(t.TempDir(), "rpcs3")
	for _, c := range []SourceConfig{
		{Type: "bbport-log", Path: bb, GameDir: `D:\Bloodborne`},
		{Type: "bbport", Path: bb},
		{Type: "rpcs3", Path: rp},
	} {
		if _, err := s.AddSource(c); err != nil {
			t.Fatal(err)
		}
	}
	at := time.Date(2026, 10, 15, 20, 0, 0, 0, time.UTC)
	// a differently written path still means the same folder
	n, err := s.StopWatching(bb+string(filepath.Separator), at)
	if err != nil || n != 2 {
		t.Fatalf("removed %d, %v", n, err)
	}
	// a fresh store reading the same files (the app restarted) sees the same thing
	again := &Store{Dir: s.Dir}
	if got := again.Sources(); len(got) != 1 || got[0].Type != "rpcs3" {
		t.Fatalf("sources after restart: %+v", got)
	}
	if !again.IsStopped(bb) || again.IsStopped(rp) {
		t.Fatal("stopped list")
	}
	st := again.StoppedFolders()
	if len(st) != 1 || len(st[0].Sources) != 2 || st[0].Sources[0].GameDir != `D:\Bloodborne` || !st[0].At.Equal(at) {
		t.Fatalf("stopped folder keeps what was configured: %+v", st)
	}

	// the last one too: nothing left configured, both remembered
	if n, err := again.StopWatching(rp, at); err != nil || n != 1 {
		t.Fatalf("last folder: %d, %v", n, err)
	}
	if len(again.Sources()) != 0 || len(again.StoppedFolders()) != 2 {
		t.Fatalf("after removing the last: %+v / %+v", again.Sources(), again.StoppedFolders())
	}
}

func TestWatchAgainRestoresWhatWasThere(t *testing.T) {
	s := testStore(t)
	bb := filepath.Join(t.TempDir(), "user")
	_, _ = s.AddSource(SourceConfig{Type: "bbport-log", Path: bb, GameDir: `D:\BB`})
	_, _ = s.StopWatching(bb, time.Now())
	if n, err := s.WatchAgain(bb); err != nil || n != 1 {
		t.Fatalf("restored %d, %v", n, err)
	}
	if got := s.Sources(); len(got) != 1 || got[0].GameDir != `D:\BB` || s.IsStopped(bb) {
		t.Fatalf("after watching again: %+v", got)
	}
	if n, _ := s.WatchAgain(bb); n != 0 {
		t.Fatal("watching again twice restores nothing more")
	}
}

func TestAddingAFolderByHandWatchesItAgain(t *testing.T) {
	s := testStore(t)
	dir := filepath.Join(t.TempDir(), "shadps4", "user")
	_, _ = s.AddSource(SourceConfig{Type: "shadps4", Path: dir})
	_, _ = s.StopWatching(dir, time.Now())
	if added, err := s.AddSource(SourceConfig{Type: "shadps4", Path: dir}); err != nil || !added {
		t.Fatalf("explicit add: %v %v", added, err)
	}
	if s.IsStopped(dir) || len(s.Sources()) != 1 {
		t.Fatal("an explicit add clears the stop")
	}
}

func TestStoppingTouchesOnlyTheConfiguration(t *testing.T) {
	s := testStore(t)
	dir := filepath.Join(t.TempDir(), "user")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	save := filepath.Join(dir, "trophies.log")
	if err := os.WriteFile(save, []byte("Runtime: trophy 1 unlocked\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	outbox := s.OutboxPath()
	if err := os.WriteFile(outbox, []byte(`{"event_id":"e1"}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := s.SetKnown(map[string]bool{"e1": true}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetToken("tok"); err != nil {
		t.Fatal(err)
	}
	_, _ = s.AddSource(SourceConfig{Type: "bbport-log", Path: dir})
	if _, err := s.StopWatching(dir, time.Now()); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(save); string(b) != "Runtime: trophy 1 unlocked\n" {
		t.Error("the game's file changed")
	}
	if b, _ := os.ReadFile(outbox); string(b) != `{"event_id":"e1"}`+"\n" {
		t.Error("the queue changed")
	}
	if !s.Known()["e1"] {
		t.Error("the history of sent events changed")
	}
	if tok, _ := s.Token(); tok != "tok" {
		t.Error("the link changed")
	}
}
