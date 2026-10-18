package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/vigil-tracker/agent/internal/config"
	"github.com/vigil-tracker/agent/internal/outbox"
)

// Exercise the actual Windows tray wiring without starting a tray, popup or real account.
func TestTrayNotificationUsesCurrentLinkAndRebuildsAfterAccountChange(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/games/") {
			if r.Header.Get("Authorization") != "" {
				t.Error("public request carried bearer")
			}
			w.Write([]byte(`{"name":"Synthetic","slug":"synthetic","selected":{"trophy_set_id":"NPWR90482_00","trophies":[{"id":9,"hidden":true,"grade":"gold"}]}}`))
			return
		}
		if r.Header.Get("Authorization") != "Bearer first-link" {
			w.WriteHeader(404)
			return
		}
		w.Write([]byte(`{"game":"Synthetic","slug":"synthetic","trophy_set_id":"NPWR90482_00","id":9,"name":"Revealed Trophy","grade":"gold","hidden":true}`))
	}))
	defer srv.Close()
	store := &config.Store{Dir: t.TempDir()}
	if err := store.SetServer(srv.URL); err != nil {
		t.Fatal(err)
	}
	if err := store.SetToken("first-link"); err != nil {
		t.Fatal(err)
	}
	n := &notifier{c: &controller{store: store, lang: "en"}}
	event := outbox.Event{TrophySetID: "NPWR90482_00", TrophyID: 9}
	b := n.builderFor(srv.URL)
	if got := b.Item(context.Background(), event); got.Trophy != "Revealed Trophy" {
		t.Fatalf("tray did not authenticate: %+v", got)
	}
	if err := store.SetToken("different-link"); err != nil {
		t.Fatal(err)
	}
	next := n.builderFor(srv.URL)
	if next == b {
		t.Fatal("tray reused the old linked client")
	}
	if got := next.Item(context.Background(), event); got.Trophy != "Trophy #9" {
		t.Fatal("account switch reused private metadata")
	}
}
