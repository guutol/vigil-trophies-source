package app

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/vigil-tracker/agent/internal/api"
	"github.com/vigil-tracker/agent/internal/config"
	"github.com/vigil-tracker/agent/internal/notify"
	"github.com/vigil-tracker/agent/internal/outbox"
	"github.com/vigil-tracker/agent/internal/source"
)

func TestHiddenPopupAfterOfflineDeliveryWithoutHistoryOrDuplicatePopups(t *testing.T) {
	user := t.TempDir()
	e, accepted, _ := gow3Engine(t, config.SourceConfig{Type: source.BBPortLog, Path: user})
	var unavailable atomic.Bool
	icon := notify.GenericIcon(notify.Gold)
	metadata := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/api/games/"):
			w.Write([]byte(`{"name":"Synthetic Game","slug":"synthetic","selected":{"trophy_set_id":"NPWR05818_00","trophies":[{"id":9,"hidden":true,"grade":"gold"},{"id":10,"hidden":true,"grade":"gold"}]}}`))
		case strings.HasPrefix(r.URL.Path, "/api/agent/trophies/"):
			if unavailable.Load() {
				w.WriteHeader(503)
				return
			}
			persisted := false
			for _, ev := range accepted.got() {
				if ev["trophy_id"] == float64(9) {
					persisted = true
				}
			}
			if !persisted || r.Header.Get("Authorization") != "Bearer vgl_test" {
				w.WriteHeader(404)
				return
			}
			json.NewEncoder(w).Encode(map[string]any{"trophy_set_id": "NPWR05818_00", "id": 9, "game": "Synthetic Game", "slug": "synthetic", "name": "Hidden Victory", "grade": "gold", "hidden": true, "icon_url": "/secret.png"})
		case r.URL.Path == "/secret.png":
			w.Header().Set("Content-Type", "image/png")
			w.Write(icon)
		default:
			w.WriteHeader(404)
		}
	}))
	defer metadata.Close()
	client, _ := api.New(metadata.URL, "vgl_test")
	b := &notify.Builder{Games: client, Icons: &notify.IconCache{Server: metadata.URL, Dir: t.TempDir()}, Lang: "en"}
	// Prefetch before unlock leaves a concealed entry in the public cache.
	b.Item(context.Background(), outbox.Event{TrophySetID: "NPWR05818_00", TrophyID: 9})
	var mu sync.Mutex
	var cards []notify.Item
	e.OnUnlocks = func(events []outbox.Event) {
		items := b.Items(context.Background(), events)
		mu.Lock()
		cards = append(cards, items...)
		mu.Unlock()
	}
	count := func() int { mu.Lock(); defer mu.Unlock(); return len(cards) }
	log := filepath.Join(user, "last_run.log")
	os.WriteFile(log, []byte("Runtime: trophy 1 unlocked\n"), 0600)
	e.Restart()
	waitFor(t, "silent historical sync", func() bool { return len(accepted.got()) == 1 })
	if count() != 0 {
		t.Fatal("historical popup")
	}
	accepted.setOffline(true)
	appendText(t, log, "Runtime: trophy 9 unlocked\n")
	waitFor(t, "offline queue", func() bool {
		s := e.Status()
		return s.Offline && s.Pending == 1
	})
	// Check once after the sender finishes the failed attempt; polling the file would
	// hold Windows read handles while the queue atomically replaces it.
	q, err := os.ReadFile(e.Store.OutboxPath())
	if err != nil || !bytes.Contains(q, []byte(`"trophy_id":9`)) {
		t.Fatal("offline event was not persisted")
	}
	if count() != 0 {
		t.Fatal("popup before server confirmation")
	}
	accepted.setOffline(false)
	waitFor(t, "resolved hidden popup", func() bool { return count() == 1 })
	mu.Lock()
	first := cards[0]
	mu.Unlock()
	if first.Trophy != "Hidden Victory" || first.Grade != notify.Gold || !bytes.Equal(first.Icon, icon) {
		t.Fatalf("popup: %+v", first)
	}
	// Metadata outage after successful ingestion keeps a safe fallback, never requeues the unlock.
	unavailable.Store(true)
	appendText(t, log, "Runtime: trophy 10 unlocked\n")
	waitFor(t, "fallback popup", func() bool { return count() == 2 })
	mu.Lock()
	fallback := cards[1]
	mu.Unlock()
	if fallback.Trophy != "Trophy #10" {
		t.Fatalf("fallback: %+v", fallback)
	}
	appendText(t, log, "Runtime: trophy 9 unlocked\nRuntime: trophy 10 unlocked\n")
	time.Sleep(300 * time.Millisecond)
	e.Restart()
	time.Sleep(300 * time.Millisecond)
	if count() != 2 || len(accepted.got()) != 3 {
		t.Fatal("metadata fallback or restart duplicated delivery/notification")
	}
}
