package syncer

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/vigil-tracker/agent/internal/api"
	"github.com/vigil-tracker/agent/internal/config"
	"github.com/vigil-tracker/agent/internal/outbox"
	"github.com/vigil-tracker/agent/internal/source"
	"github.com/vigil-tracker/agent/internal/source/bbport"
	"github.com/vigil-tracker/agent/internal/trophyfile"
)

// fakeServer records every event_id it receives and answers with the configured status.
type fakeServer struct {
	mu       sync.Mutex
	status   int
	reject   map[string]string
	received [][]string
	events   []map[string]any
}

func (f *fakeServer) handler(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Events []map[string]any `json:"events"`
	}
	json.NewDecoder(r.Body).Decode(&body)
	f.mu.Lock()
	defer f.mu.Unlock()
	var ids []string
	var rejected []map[string]string
	for _, e := range body.Events {
		id, _ := e["event_id"].(string)
		ids = append(ids, id)
		f.events = append(f.events, e)
		if reason, ok := f.reject[id]; ok {
			rejected = append(rejected, map[string]string{"event_id": id, "reason": reason})
		}
	}
	f.received = append(f.received, ids)
	if f.status != 0 && f.status != 200 {
		w.WriteHeader(f.status)
		return
	}
	json.NewEncoder(w).Encode(map[string]any{"accepted": len(ids) - len(rejected), "duplicates": 0, "rejected": rejected})
}

type env struct {
	t       *testing.T
	userDir string
	store   *config.Store
	server  *fakeServer
	url     string
}

func newEnv(t *testing.T) *env {
	t.Helper()
	cfg := t.TempDir()
	t.Setenv("VIGIL_CONFIG_DIR", cfg)
	store, err := config.Open()
	if err != nil {
		t.Fatal(err)
	}
	fs := &fakeServer{reject: map[string]string{}}
	srv := httptest.NewServer(http.HandlerFunc(fs.handler))
	t.Cleanup(srv.Close)
	return &env{t: t, userDir: t.TempDir(), store: store, server: fs, url: srv.URL}
}

func (e *env) appendLines(ids ...int) {
	e.t.Helper()
	f, err := os.OpenFile(filepath.Join(e.userDir, trophyfile.FileName), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		e.t.Fatal(err)
	}
	defer f.Close()
	for _, id := range ids {
		data, _ := json.Marshal(map[string]any{"v": 1, "title_id": "CUSA03173", "trophy_id": id,
			"unlocked_at": "2026-10-10T06:30:00Z"})
		f.Write(append(data, '\n'))
	}
}

func (e *env) syncer(extra ...source.Source) *Syncer {
	e.t.Helper()
	client, err := api.New(e.url, "vgl_test")
	if err != nil {
		e.t.Fatal(err)
	}
	bb, err := bbport.New(e.userDir)
	if err != nil {
		e.t.Fatal(err)
	}
	s, err := New(client, e.store, append([]source.Source{bb}, extra...), log.New(io.Discard, "", 0))
	if err != nil {
		e.t.Fatal(err)
	}
	return s
}

// fakeSource returns a fixed list of unlocks (or an error).
type fakeSource struct {
	name    string
	unlocks []source.Unlock
	err     error
	calls   int
}

func (f *fakeSource) Name() string { return f.name }
func (f *fakeSource) Scan(context.Context) ([]source.Unlock, error) {
	f.calls++
	return f.unlocks, f.err
}

var bg = context.Background()

func TestRestartKeepsEventIDs(t *testing.T) {
	e := newEnv(t)
	e.appendLines(1, 2, 3)
	e.server.status = http.StatusInternalServerError

	first := e.syncer()
	if err := first.Scan(bg); err != nil {
		t.Fatal(err)
	}
	if err := first.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	if first.Pending() != 3 || len(e.server.received) != 1 {
		t.Fatalf("after a 500 the events must stay queued: pending=%d calls=%d", first.Pending(), len(e.server.received))
	}
	sentFirst := e.server.received[0]

	// "restart": a new syncer on the same config dir, server back up
	e.server.status = http.StatusOK
	second := e.syncer()
	if err := second.Scan(bg); err != nil {
		t.Fatal(err)
	}
	if second.Pending() != 3 {
		t.Fatalf("restart must not read the lines again: pending=%d", second.Pending())
	}
	if err := second.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	sentSecond := e.server.received[1]
	if len(sentSecond) != 3 {
		t.Fatalf("want 3 events resent, got %v", sentSecond)
	}
	for i := range sentFirst {
		if sentFirst[i] != sentSecond[i] {
			t.Fatalf("event_id changed across restart: %s vs %s", sentFirst[i], sentSecond[i])
		}
	}
	if second.Pending() != 0 {
		t.Fatalf("outbox should be empty, has %d", second.Pending())
	}
}

func TestCrashBeforeKnownIDsAreSavedDoesNotRequeue(t *testing.T) {
	e := newEnv(t)
	e.appendLines(1, 2)
	e.server.status = http.StatusServiceUnavailable
	s := e.syncer()
	s.Scan(bg)
	// simulate a crash right after the outbox was written: known.json never saved
	os.Remove(filepath.Join(e.store.Dir, "known.json"))

	again := e.syncer()
	again.Scan(bg)
	if again.Pending() != 2 {
		t.Fatalf("lines already in the outbox were queued again: pending=%d", again.Pending())
	}
}

func TestSentUnlocksAreNotResentAfterRestart(t *testing.T) {
	e := newEnv(t)
	e.appendLines(1, 2)
	s := e.syncer()
	s.Scan(bg)
	s.Flush(bg)
	if s.Pending() != 0 || len(e.server.received) != 1 {
		t.Fatalf("first send: pending=%d calls=%d", s.Pending(), len(e.server.received))
	}
	again := e.syncer()
	again.Scan(bg) // reads trophies.jsonl from the start: same ids, all known
	again.Flush(bg)
	if again.Pending() != 0 || len(e.server.received) != 1 {
		t.Fatalf("known unlocks were sent again: calls=%d", len(e.server.received))
	}
	e.appendLines(3)
	again.Scan(bg)
	if again.Pending() != 1 {
		t.Fatalf("new line not queued: pending=%d", again.Pending())
	}
}

func TestEventIDsAreDeterministic(t *testing.T) {
	u := source.Unlock{Source: "rpcs3", TrophySetID: "NPWR00001_00", TrophyID: 7}
	if source.EventID(u) != source.EventID(u) {
		t.Fatal("same unlock, different ids")
	}
	other := u
	other.Source = "shadps4"
	byTitle := source.Unlock{Source: "bbport", TitleID: "NPWR00001_00", TrophyID: 7}
	if source.EventID(other) == source.EventID(u) || source.EventID(byTitle) == source.EventID(u) {
		t.Fatal("different sources must give different ids")
	}
	timed := u
	timed.UnlockedAt = "2026-10-10T06:30:00Z"
	if source.EventID(timed) != source.EventID(u) {
		t.Fatal("the unlock time is not part of the id")
	}
}

func TestEverySourceIsScannedAndSentWithItsIDs(t *testing.T) {
	e := newEnv(t)
	e.appendLines(4)
	rp := &fakeSource{name: "rpcs3", unlocks: []source.Unlock{
		{Source: "rpcs3", TrophySetID: "NPWR00001_00", TrophyID: 1, UnlockedAt: "2026-10-10T06:30:00Z"},
		{Source: "rpcs3", TrophySetID: "NPWR00001_00", TrophyID: 1}, // same trophy twice: one event
	}}
	broken := &fakeSource{name: "shadps4", err: errors.New("x.xml: not a trophy XML file"),
		unlocks: []source.Unlock{{Source: "shadps4", TrophySetID: "NPWR00002_00", TitleID: "CUSA00002", TrophyID: 2}}}
	s := e.syncer(rp, broken)
	if err := s.Scan(bg); err != nil {
		t.Fatal(err)
	}
	if s.Pending() != 3 {
		t.Fatalf("want bbport + rpcs3 + shadps4 = 3 events, got %d", s.Pending())
	}
	s.Flush(bg)
	byID := map[string]map[string]any{}
	for _, ev := range e.server.events {
		byID[ev["event_id"].(string)] = ev
	}
	want := source.Unlock{Source: "rpcs3", TrophySetID: "NPWR00001_00", TrophyID: 1}
	got := byID[source.EventID(want)]
	if got == nil || got["source"] != "rpcs3" || got["trophy_set_id"] != "NPWR00001_00" || got["title_id"] != nil {
		t.Fatalf("rpcs3 event: %v", got)
	}
	bb := byID[source.EventID(source.Unlock{Source: "bbport", TitleID: "CUSA03173", TrophyID: 4})]
	if bb == nil || bb["title_id"] != "CUSA03173" || bb["trophy_set_id"] != nil || bb["source"] != "bbport" {
		t.Fatalf("bbport event: %v", bb)
	}
	sh := byID[source.EventID(source.Unlock{Source: "shadps4", TrophySetID: "NPWR00002_00", TrophyID: 2})]
	if sh == nil || sh["title_id"] != "CUSA00002" {
		t.Fatalf("shadps4 event: %v", sh)
	}
}

func TestOldOutboxEventsAreStillSent(t *testing.T) {
	e := newEnv(t)
	old := `{"event_id":"0b0e5d2c-6a43-4c3c-9d55-0f3e0a7d1a11","title_id":"CUSA03173","trophy_id":5,` +
		`"unlocked_at":"2026-10-10T06:30:00Z","source_end":83}` + "\n"
	os.WriteFile(e.store.OutboxPath(), []byte(old), 0o600)
	s := e.syncer()
	s.Flush(bg)
	if s.Pending() != 0 || len(e.server.events) != 1 || e.server.events[0]["title_id"] != "CUSA03173" {
		t.Fatalf("old event: pending=%d events=%v", s.Pending(), e.server.events)
	}
	if _, ok := e.server.events[0]["source_end"]; ok {
		t.Fatal("source_end must not be sent")
	}
}

func TestBackoffAfterFailureAndNoBusyRetry(t *testing.T) {
	e := newEnv(t)
	e.appendLines(1)
	e.server.status = http.StatusBadGateway
	s := e.syncer()
	now := time.Date(2026, 10, 10, 6, 0, 0, 0, time.UTC)
	s.Now = func() time.Time { return now }
	s.Scan(bg)
	s.Flush(context.Background())
	s.Flush(context.Background()) // too early: must not call the server again
	if len(e.server.received) != 1 {
		t.Fatalf("retried before the backoff elapsed: %d calls", len(e.server.received))
	}
	now = now.Add(2 * time.Second) // first backoff is at most 1 s
	e.server.status = http.StatusOK
	s.Flush(context.Background())
	if len(e.server.received) != 2 || s.Pending() != 0 {
		t.Fatalf("should have retried and emptied the outbox: calls=%d pending=%d", len(e.server.received), s.Pending())
	}
}

func TestRetryAfterIsRespected(t *testing.T) {
	e := newEnv(t)
	e.appendLines(1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "30")
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()
	e.url = srv.URL
	s := e.syncer()
	now := time.Date(2026, 10, 10, 6, 0, 0, 0, time.UTC)
	s.Now = func() time.Time { return now }
	s.Scan(bg)
	s.Flush(context.Background())
	if !s.nextSend.Equal(now.Add(30 * time.Second)) {
		t.Fatalf("next attempt should be in 30 s, got %v", s.nextSend.Sub(now))
	}
}

func TestUnauthorizedStops(t *testing.T) {
	e := newEnv(t)
	e.appendLines(1)
	e.server.status = http.StatusUnauthorized
	s := e.syncer()
	s.Scan(bg)
	if err := s.Flush(context.Background()); !errors.Is(err, api.ErrUnauthorized) {
		t.Fatalf("want ErrUnauthorized, got %v", err)
	}
	if s.Pending() != 1 {
		t.Fatal("events must be kept for after the next link")
	}
}

func TestRejectedEventsLeaveTheOutbox(t *testing.T) {
	e := newEnv(t)
	e.appendLines(1, 2)
	s := e.syncer()
	s.Scan(bg)
	e.server.reject[s.Outbox.Peek(1)[0].EventID] = "unknown_trophy"
	if err := s.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	if s.Pending() != 0 {
		t.Fatalf("rejected event should be dropped, pending=%d", s.Pending())
	}
}

func TestWholeBatch400IsDropped(t *testing.T) {
	e := newEnv(t)
	e.appendLines(1)
	e.server.status = http.StatusBadRequest
	s := e.syncer()
	s.Scan(bg)
	s.Flush(context.Background())
	if s.Pending() != 0 {
		t.Fatal("a 400 batch would fail forever; it must be dropped")
	}
}

func TestBatchesOfAtMost100(t *testing.T) {
	e := newEnv(t)
	ids := make([]int, 0, 250)
	for i := 0; i < 250; i++ {
		ids = append(ids, i%256)
	}
	e.appendLines(ids...)
	s := e.syncer()
	s.Scan(bg)
	s.Flush(context.Background())
	if len(e.server.received) != 3 || len(e.server.received[0]) != 100 || len(e.server.received[2]) != 50 {
		t.Fatalf("want batches 100/100/50, got %d calls", len(e.server.received))
	}
}

func TestBackoffGrowsAndCaps(t *testing.T) {
	b := &Backoff{Min: time.Second, Max: 5 * time.Minute, rand: func() float64 { return 1 }}
	want := []time.Duration{time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second}
	for _, w := range want {
		if d := b.Next(); d != w {
			t.Fatalf("got %v want %v", d, w)
		}
	}
	for i := 0; i < 50; i++ {
		b.Next()
	}
	if d := b.Next(); d != 5*time.Minute {
		t.Fatalf("cap: got %v", d)
	}
	b.Reset()
	b.rand = func() float64 { return 0 }
	if d := b.Next(); d != 500*time.Millisecond {
		t.Fatalf("jitter floor: got %v", d)
	}
}

func TestOnDeliveredReportsOnlyLiveAcceptedUnlocks(t *testing.T) {
	e := newEnv(t)
	e.appendLines(1, 2) // already there when the agent starts: backlog, no notification
	s := e.syncer()
	var got [][]int
	s.OnDelivered = func(events []outbox.Event) {
		var ids []int
		for _, ev := range events {
			ids = append(ids, ev.TrophyID)
		}
		got = append(got, ids)
	}
	s.Scan(bg)
	s.Flush(bg)
	if len(got) != 0 {
		t.Fatalf("backlog must not be reported, got %v", got)
	}
	e.appendLines(3, 4) // unlocked while running
	s.Scan(bg)
	refused := source.EventID(source.Unlock{Source: "bbport", TitleID: "CUSA03173", TrophyID: 4})
	e.server.reject[refused] = "unknown_trophy"
	s.Flush(bg)
	if len(got) != 1 || len(got[0]) != 1 || got[0][0] != 3 {
		t.Fatalf("want only trophy 3 (4 was rejected), got %v", got)
	}
	s.Scan(bg) // nothing new
	s.Flush(bg)
	if len(got) != 1 {
		t.Fatalf("delivered twice: %v", got)
	}
}

func TestOnDeliveredWaitsForTheServer(t *testing.T) {
	e := newEnv(t)
	s := e.syncer()
	n := 0
	s.OnDelivered = func(events []outbox.Event) { n += len(events) }
	s.Scan(bg)
	e.appendLines(7)
	s.Scan(bg)
	e.server.status = http.StatusServiceUnavailable
	s.Flush(bg)
	if n != 0 {
		t.Fatal("reported before the server accepted it")
	}
	e.server.status = 0
	s.nextSend = time.Time{}
	s.Flush(bg)
	if n != 1 {
		t.Fatalf("want 1 after the retry, got %d", n)
	}
}

func TestOnConnectionReportsOutagesWithoutLosingEvents(t *testing.T) {
	e := newEnv(t)
	e.appendLines(1, 2)
	e.server.status = http.StatusServiceUnavailable
	s := e.syncer()
	now := time.Date(2026, 10, 13, 6, 0, 0, 0, time.UTC)
	s.Now = func() time.Time { return now }
	type report struct {
		ok      bool
		pending int
	}
	var got []report
	s.OnConnection = func(ok bool, pending int) { got = append(got, report{ok, pending}) }
	s.Scan(bg)
	s.Flush(context.Background())
	if len(got) != 1 || got[0].ok || got[0].pending != 2 || s.Pending() != 2 {
		t.Fatalf("outage: want one report (false, 2) and 2 queued, got %v queued=%d", got, s.Pending())
	}
	now = now.Add(time.Minute)
	e.server.status = http.StatusOK
	s.Flush(context.Background())
	if last := got[len(got)-1]; !last.ok || last.pending != 0 || s.Pending() != 0 {
		t.Fatalf("back online: want (true, 0) and an empty queue, got %v queued=%d", got, s.Pending())
	}
}
