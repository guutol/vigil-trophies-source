package syncer

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/vigil-tracker/agent/internal/api"
	"github.com/vigil-tracker/agent/internal/catalog"
	"github.com/vigil-tracker/agent/internal/config"
)

// fakeCatalogServer answers catalog-status and records proposals.
type fakeCatalogServer struct {
	mu       sync.Mutex
	known    map[string]bool
	status   int // for proposals; 0 = 200
	statusFn func(set string) int
	received []string
}

func (f *fakeCatalogServer) handler(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	switch r.URL.Path {
	case "/api/agent/catalog-status":
		var known []string
		for _, id := range strings.Split(r.URL.Query().Get("sets"), ",") {
			if f.known[id] {
				known = append(known, id)
			}
		}
		json.NewEncoder(w).Encode(map[string]any{"known": known, "proposed": []string{}})
	case "/api/agent/catalog-proposals":
		var p catalog.Proposal
		json.NewDecoder(r.Body).Decode(&p)
		f.received = append(f.received, p.TrophySetID)
		code := f.status
		if f.statusFn != nil {
			code = f.statusFn(p.TrophySetID)
		}
		if code != 0 && code != 200 {
			w.WriteHeader(code)
			json.NewEncoder(w).Encode(map[string]string{"error": "invalid_proposal"})
			return
		}
		json.NewEncoder(w).Encode(map[string]string{"status": "pending"})
	}
}

type fixedProvider []catalog.Dir

func (f fixedProvider) CatalogDirs() []catalog.Dir { return f }

func rpcs3Dir(t *testing.T, set string) catalog.Dir {
	t.Helper()
	dir := filepath.Join(t.TempDir(), set)
	os.MkdirAll(dir, 0o700)
	xml := `<trophyconf><npcommid>` + set + `</npcommid><title-name>Game</title-name>` +
		`<trophy id="0" ttype="B" hidden="no"><name>A</name><detail>a</detail></trophy></trophyconf>`
	os.WriteFile(filepath.Join(dir, "TROPCONF.SFM"), []byte(xml), 0o600)
	return catalog.Dir{SetID: set, Kind: catalog.KindRPCS3, Path: dir}
}

func newProposer(t *testing.T, fs *fakeCatalogServer, dirs ...catalog.Dir) (*Proposer, *config.Store) {
	t.Helper()
	t.Setenv("VIGIL_CONFIG_DIR", t.TempDir())
	store, _ := config.Open()
	srv := httptest.NewServer(http.HandlerFunc(fs.handler))
	t.Cleanup(srv.Close)
	client, _ := api.New(srv.URL, "vgl_test")
	return NewProposer(client, store, []catalog.Provider{fixedProvider(dirs)}, log.New(io.Discard, "", 0)), store
}

func TestProposerSendsOnlyUnknownSetsOnceAndRemembersThem(t *testing.T) {
	fs := &fakeCatalogServer{known: map[string]bool{"NPWR00001_00": true}}
	p, store := newProposer(t, fs, rpcs3Dir(t, "NPWR00001_00"), rpcs3Dir(t, "NPWR00002_00"))
	now := time.Date(2026, 10, 11, 0, 0, 0, 0, time.UTC)
	p.Now = func() time.Time { return now }
	if err := p.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(fs.received) != 1 || fs.received[0] != "NPWR00002_00" {
		t.Fatalf("sent %v", fs.received)
	}
	if h := store.HandledCatalogs(); !h["NPWR00001_00"] || !h["NPWR00002_00"] {
		t.Fatalf("handled %v", h)
	}
	now = now.Add(ProposeEvery + time.Second)
	p.Tick(context.Background())
	if len(fs.received) != 1 {
		t.Fatal("a handled set was proposed again")
	}
	// a new agent run reads catalogs.json and does not resend either
	again := NewProposer(p.Client, store, p.Providers, p.Log)
	again.Tick(context.Background())
	if len(fs.received) != 1 {
		t.Fatal("resent after restart")
	}
}

func TestProposerWaitsBetweenRoundsAndLimitsEachRound(t *testing.T) {
	var dirs []catalog.Dir
	for i := 0; i < 7; i++ {
		dirs = append(dirs, rpcs3Dir(t, "NPWR0001"+string(rune('0'+i))+"_00"))
	}
	fs := &fakeCatalogServer{known: map[string]bool{}}
	p, _ := newProposer(t, fs, dirs...)
	now := time.Date(2026, 10, 11, 0, 0, 0, 0, time.UTC)
	p.Now = func() time.Time { return now }
	p.Tick(context.Background())
	if len(fs.received) != maxProposalsPerRound {
		t.Fatalf("first round sent %d", len(fs.received))
	}
	p.Tick(context.Background()) // too early
	if len(fs.received) != maxProposalsPerRound {
		t.Fatal("ran again before the interval")
	}
	now = now.Add(ProposeEvery)
	p.Tick(context.Background())
	if len(fs.received) != 7 {
		t.Fatalf("second round: total %d", len(fs.received))
	}
}

func TestProposerKeepsSetsForLaterOnTemporaryErrors(t *testing.T) {
	fs := &fakeCatalogServer{known: map[string]bool{}, statusFn: func(set string) int {
		if set == "NPWR00021_00" {
			return http.StatusBadRequest // refused for good
		}
		return http.StatusTooManyRequests // try later
	}}
	p, store := newProposer(t, fs, rpcs3Dir(t, "NPWR00021_00"), rpcs3Dir(t, "NPWR00022_00"))
	p.Tick(context.Background())
	h := store.HandledCatalogs()
	if !h["NPWR00021_00"] || h["NPWR00022_00"] {
		t.Fatalf("handled %v", h)
	}
}

func TestProposerStopsOnUnauthorized(t *testing.T) {
	fs := &fakeCatalogServer{known: map[string]bool{}, status: http.StatusUnauthorized}
	p, _ := newProposer(t, fs, rpcs3Dir(t, "NPWR00031_00"))
	if err := p.Tick(context.Background()); err != api.ErrUnauthorized {
		t.Fatalf("want ErrUnauthorized, got %v", err)
	}
}

func TestBrokenFoldersAreSkipped(t *testing.T) {
	broken := catalog.Dir{SetID: "NPWR00041_00", Kind: catalog.KindRPCS3, Path: t.TempDir()}
	fs := &fakeCatalogServer{known: map[string]bool{}}
	p, store := newProposer(t, fs, broken)
	p.Tick(context.Background())
	if len(fs.received) != 0 || !store.HandledCatalogs()["NPWR00041_00"] {
		t.Fatal("a folder without texts must be skipped once and remembered")
	}
}
