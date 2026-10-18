package app

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/vigil-tracker/agent/internal/config"
	"github.com/vigil-tracker/agent/internal/source"
	"github.com/vigil-tracker/agent/internal/source/bbportlog"
	"github.com/vigil-tracker/agent/internal/source/detect"
)

func sotcDump(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "sce_sys"), 0700)
	os.WriteFile(filepath.Join(dir, "sce_sys", "param.sfo"), testSFO(map[string]string{"TITLE_ID": "CUSA08809", "TITLE": "SHADOW OF THE COLOSSUS"}), 0600)
	os.WriteFile(filepath.Join(dir, "sce_sys", "npbind.dat"), []byte("\x00NPWR90302_00\x00"), 0600)
	return dir
}

func TestSOTCEngineLogQueueAndNotification(t *testing.T) {
	user, game := t.TempDir(), sotcDump(t)
	e, api, notifications := gow3Engine(t, config.SourceConfig{Type: source.SOTCLog, Path: user, GameDir: game})
	p := filepath.Join(user, bbportlog.LogName)
	os.WriteFile(p, []byte("Runtime: trophy 1 unlocked\n"), 0600)
	e.Restart()
	waitFor(t, "silent historical trophy", func() bool { return len(api.got()) == 1 })
	if len(notifications.got()) != 0 {
		t.Fatal("historical trophy notified")
	}
	api.setOffline(true)
	os.WriteFile(p, []byte("New session\n"), 0600)
	appendText(t, p, "Runtime: trophy 2 unloc")
	time.Sleep(200 * time.Millisecond)
	if len(api.got()) != 1 {
		t.Fatal("partial line sent")
	}
	appendText(t, p, "ked\n")
	waitFor(t, "offline event persisted", func() bool {
		q, err := os.ReadFile(e.Store.OutboxPath())
		return err == nil && bytes.Contains(q, []byte(`"trophy_id":2`))
	})
	api.setOffline(false)
	waitFor(t, "API delivery", func() bool { return len(api.got()) == 2 })
	waitFor(t, "live notification", func() bool { return len(notifications.got()) == 1 })
	for _, ev := range api.got() {
		if ev["source"] != source.SOTCLog || ev["trophy_set_id"] != "NPWR90302_00" || ev["title_id"] != "CUSA08809" {
			t.Fatalf("wrong game: %+v", ev)
		}
	}
	appendText(t, p, "Runtime: trophy 2 unlocked\n")
	time.Sleep(300 * time.Millisecond)
	if len(api.got()) != 2 {
		t.Fatal("duplicate trophy sent")
	}
}

func TestSOTCRejectsWrongGameAndMissingMetadata(t *testing.T) {
	isolateLaunchers(t)
	for _, dir := range []string{"", t.TempDir(), gow3Dump(t, "CUSA01623", testGOW3Set)} {
		if _, err := bbportlog.IdentifySOTC(dir); !errors.Is(err, bbportlog.ErrSOTCUnidentified) {
			t.Fatalf("wrong identity accepted %q: %v", dir, err)
		}
	}
	user := t.TempDir()
	os.WriteFile(filepath.Join(user, bbportlog.LogName), []byte("Runtime: trophy 1 unlocked\n"), 0600)
	e, api, _ := gow3Engine(t, config.SourceConfig{Type: source.SOTCLog, Path: user})
	e.Restart()
	time.Sleep(300 * time.Millisecond)
	if len(api.got()) != 0 {
		t.Fatal("unidentified source sent events")
	}
	s := Summarize(context.Background(), config.SourceConfig{Type: source.SOTCLog, Path: user})
	if !errors.Is(s.Err, bbportlog.ErrSOTCUnidentified) {
		t.Fatalf("missing diagnostic: %v", s.Err)
	}
	for _, lang := range []string{"en", "pt-BR"} {
		tx := Texts(lang)
		if Folders([]Summary{s})[0].Status(tx, Status{State: Running}) != tx.T("folder_status_sotc_unidentified") {
			t.Fatal("wrong source diagnostic")
		}
	}
}

func TestSOTCStoppedAndLegacySourceReplacement(t *testing.T) {
	store := newStore(t)
	dir := t.TempDir()
	store.AddSource(config.SourceConfig{Type: source.BBPortLog, Path: dir})
	found := []detect.Candidate{{Type: source.SOTCLog, Path: dir, Certain: true}}
	plan := PlanDetection(store.Sources(), found)
	if len(plan.Replace) != 1 || len(plan.Add) != 1 {
		t.Fatalf("legacy source not replaced: %+v", plan)
	}
	store.StopWatching(dir, time.Now())
	if len(WithoutStopped(found, store.StoppedFolders())) != 0 {
		t.Fatal("stopped folder was offered")
	}
}

func TestSOTCBloodborneAndGOW3KeepSeparateIdentities(t *testing.T) {
	sotc, bb, gow := t.TempDir(), t.TempDir(), t.TempDir()
	e, api, _ := gow3Engine(t,
		config.SourceConfig{Type: source.SOTCLog, Path: sotc, GameDir: sotcDump(t)},
		config.SourceConfig{Type: source.BBPortLog, Path: bb},
		config.SourceConfig{Type: source.GOW3Log, Path: gow, GameDir: gow3Dump(t, "CUSA01623", testGOW3Set)})
	for _, dir := range []string{sotc, bb, gow} {
		os.WriteFile(filepath.Join(dir, bbportlog.LogName), []byte("Runtime: trophy 5 unlocked\n"), 0600)
	}
	e.Restart()
	waitFor(t, "three independent games", func() bool { return len(api.got()) == 3 })
	sets := map[any]bool{}
	events := map[any]bool{}
	for _, ev := range api.got() {
		sets[ev["trophy_set_id"]] = true
		events[ev["event_id"]] = true
		if ev["source"] == source.SOTCLog && ev["trophy_set_id"] != "NPWR90302_00" {
			t.Fatalf("Shadow reported as another game: %+v", ev)
		}
	}
	if len(sets) != 3 || len(events) != 3 {
		t.Fatalf("games were mixed: %+v", api.got())
	}
	// A new agent session reads the same files without producing duplicate API events.
	e.Restart()
	time.Sleep(300 * time.Millisecond)
	if len(api.got()) != 3 {
		t.Fatal("restart duplicated unlocks")
	}
}
