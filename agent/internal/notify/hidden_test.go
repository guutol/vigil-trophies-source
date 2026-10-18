package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/vigil-tracker/agent/internal/api"
	"github.com/vigil-tracker/agent/internal/outbox"
)

// Real HTTP client + builder: cache a concealed public entry, unlock, then resolve it privately.
func TestHiddenMetadataBypassesPublicCacheAndIsNeverReusedAcrossAccounts(t *testing.T) {
	var unlocked atomic.Bool
	var offline atomic.Bool
	var publicUnavailable atomic.Bool
	var publicCalls, privateCalls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/games/NPWR90001_00":
			publicCalls.Add(1)
			if publicUnavailable.Load() {
				w.WriteHeader(503)
				return
			}
			if r.Header.Get("Authorization") != "" {
				t.Error("bearer sent to public catalog")
			}
			w.Write([]byte(`{"name":"Test Game","slug":"test-game","selected":{"trophy_set_id":"NPWR90001_00","trophies":[{"id":1,"hidden":true,"grade":"gold"},{"id":2,"name":"Normal","grade":"silver"}]}}`))
		case "/api/agent/trophies/NPWR90001_00/1":
			privateCalls.Add(1)
			if offline.Load() {
				w.WriteHeader(503)
				return
			}
			if !unlocked.Load() || r.Header.Get("Authorization") != "Bearer owner" {
				w.WriteHeader(404)
				return
			}
			name := "Secret Name"
			if r.URL.Query().Get("lang") == "pt-BR" {
				name = "Nome Secreto"
			}
			json.NewEncoder(w).Encode(map[string]any{"trophy_set_id": "NPWR90001_00", "id": 1,
				"name": name, "hidden": true, "grade": "gold", "icon_url": "/secret.png", "game": "Test Game", "slug": "test-game"})
		default:
			w.WriteHeader(404)
		}
	}))
	defer srv.Close()
	client, _ := api.New(srv.URL, "owner")
	icon := []byte("synthetic icon")
	b := &Builder{Games: client, Icons: fakeIcons{map[string][]byte{"/secret.png": icon}}, Lang: "en"}
	e := outbox.Event{TrophySetID: "NPWR90001_00", TrophyID: 1}
	if got := b.Item(context.Background(), e); got.Trophy != "Trophy #1" {
		t.Fatal("locked name leaked")
	}
	unlocked.Store(true)
	if got := b.Item(context.Background(), e); got.Trophy != "Secret Name" || got.Grade != Gold || !bytes.Equal(got.Icon, icon) {
		t.Fatalf("unlocked: %+v", got)
	}
	if publicCalls.Load() != 1 {
		t.Fatal("public cache was unnecessarily replaced")
	}
	b.SetLang("pt-BR")
	if got := b.Item(context.Background(), e); got.Trophy != "Nome Secreto" {
		t.Fatal("wrong language")
	}
	client.Token = "another-account"
	if got := b.Item(context.Background(), e); got.Trophy != "Troféu #1" {
		t.Fatal("private cache leaked another account's name")
	}
	client.Token = "owner"
	offline.Store(true)
	if got := b.Item(context.Background(), e); got.Trophy != "Troféu #1" || got.Grade != Gold {
		t.Fatal("unsafe unavailable-metadata fallback")
	}
	offline.Store(false)
	if got := b.Item(context.Background(), e); got.Trophy != "Nome Secreto" {
		t.Fatal("failure cached across confirmed unlocks")
	}
	before := privateCalls.Load()
	b.Item(context.Background(), outbox.Event{TrophySetID: "NPWR90001_00", TrophyID: 2})
	if privateCalls.Load() != before {
		t.Fatal("ordinary trophy required extra authenticated request")
	}
	publicUnavailable.Store(true)
	withoutPublic := &Builder{Games: client, Lang: "en"}
	if got := withoutPublic.Item(context.Background(), e); got.Trophy != "Secret Name" || got.Game != "Test Game" {
		t.Fatal("public lookup outage blocked confirmed private metadata")
	}
}
