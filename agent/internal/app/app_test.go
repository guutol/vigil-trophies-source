package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
	"github.com/vigil-tracker/agent/internal/config"
	"github.com/vigil-tracker/agent/internal/outbox"
	"github.com/vigil-tracker/agent/internal/source"
	"github.com/vigil-tracker/agent/internal/source/detect"
	"github.com/vigil-tracker/agent/internal/syncer"
	"github.com/vigil-tracker/agent/internal/trophyfile"
)

// ---- autostart ----

type mapKey struct {
	values map[string]string
	fail   error
}

func (m *mapKey) Get(name string) (string, bool, error) {
	if m.fail != nil {
		return "", false, m.fail
	}
	v, ok := m.values[name]
	return v, ok, nil
}
func (m *mapKey) Set(name, value string) error { m.values[name] = value; return m.fail }
func (m *mapKey) Delete(name string) error     { delete(m.values, name); return m.fail }

func TestAutostartOnOff(t *testing.T) {
	key := &mapKey{values: map[string]string{"Other": "x"}}
	a := Autostart{Key: key, Exe: `C:\Program Files\Vigil\vigil-agent-tray.exe`}
	if on, _ := a.Enabled(); on {
		t.Fatal("off by default")
	}
	if err := a.SetEnabled(true); err != nil {
		t.Fatal(err)
	}
	if got := key.values[AutostartName]; got != `"C:\Program Files\Vigil\vigil-agent-tray.exe"` {
		t.Fatalf("path must be quoted, got %q", got)
	}
	if on, _ := a.Enabled(); !on {
		t.Fatal("should be on")
	}
	// Another copy somewhere else does not count as "this app starts with Windows".
	moved := Autostart{Key: key, Exe: `D:\Vigil\vigil-agent-tray.exe`}
	if on, _ := moved.Enabled(); on {
		t.Fatal("a different exe path is not this app")
	}
	if err := a.SetEnabled(false); err != nil {
		t.Fatal(err)
	}
	if _, ok := key.values[AutostartName]; ok {
		t.Fatal("value should be removed")
	}
	if key.values["Other"] != "x" {
		t.Fatal("other entries must not be touched")
	}
	if err := (Autostart{Key: key, Exe: `C:\a"b.exe`}).SetEnabled(true); err == nil {
		t.Fatal("a path with quotes must be refused")
	}
	key.fail = errors.New("access denied")
	if _, err := a.Enabled(); err == nil {
		t.Fatal("registry errors are reported")
	}
}

func TestLangAndTexts(t *testing.T) {
	for locale, want := range map[string]string{"pt-BR": "pt-BR", "pt-PT": "pt-BR", "en-US": "en", "de-DE": "en", "": "en"} {
		if Lang(locale) != want {
			t.Errorf("Lang(%q) = %q", locale, Lang(locale))
		}
	}
	pt := Texts("pt-BR")
	if got := pt.SourceCounts("rpcs3", 1, 1); got != "RPCS3 (beta) — 1 jogo, 1 troféu" {
		t.Errorf("got %q", got)
	}
	if got := Texts("en").SourceCounts("bbport-log", 2, 30); got != "bbport (log) — 2 games, 30 trophies" {
		t.Errorf("got %q", got)
	}
	if Texts("klingon").Lang != "en" {
		t.Error("unknown language falls back to English")
	}
}

// ---- menu ----

func labels(items []Item) string {
	var b strings.Builder
	for _, it := range items {
		b.WriteString(it.Label + "|")
		for _, s := range it.Sub {
			b.WriteString("  " + s.Label + "|")
		}
	}
	return b.String()
}

func TestMenuStates(t *testing.T) {
	pt := Texts("pt-BR")
	m := BuildMenu(pt, MenuState{Status: Status{State: NotLinked}})
	s := labels(m)
	if !strings.Contains(s, "Não vinculado|") || !strings.Contains(s, "Vincular este PC|") {
		t.Fatalf("not linked: %s", s)
	}
	if find(m, CmdProfile).Disabled == false || find(m, CmdPause).Disabled == false {
		t.Fatal("profile and pause need a linked PC")
	}

	dir := t.TempDir()
	m = BuildMenu(pt, MenuState{Status: Status{State: Running}, Username: "guto", ProfileURL: "https://x.test/u/guto",
		Autostart: true, Summaries: []Summary{{Config: config.SourceConfig{Type: "rpcs3", Path: dir}, Games: 2, Unlocks: 30}}})
	s = labels(m)
	for _, want := range []string{"Vinculado como guto|", "Sincronizando|", "Gerenciar pastas|", "  RPCS3 (beta)|",
		"Pausar sincronização|", "Iniciar com o Windows|", "Sair|"} {
		if !strings.Contains(s, want) {
			t.Errorf("running menu misses %q:\n%s", want, s)
		}
	}
	if find(m, CmdLink).ID != 0 {
		t.Error("no link entry once linked")
	}
	if !find(m, CmdAutostart).Checked || find(m, CmdProfile).Disabled {
		t.Error("autostart checked, profile enabled")
	}

	// the server cannot be reached: say so (and how much is waiting) instead of "syncing"
	m = BuildMenu(pt, MenuState{Status: Status{State: Running, Offline: true, Pending: 3}, Username: "guto"})
	if s := labels(m); !strings.Contains(s, "Sem conexão com o servidor: 3 na fila, tentando de novo|") ||
		strings.Contains(s, "Sincronizando|") {
		t.Errorf("offline menu: %s", s)
	}

	m = BuildMenu(pt, MenuState{Status: Status{State: Running, Paused: true}, Username: "guto"})
	if find(m, CmdPause).Label != "Retomar sincronização" || !strings.Contains(labels(m), "Pausado|") {
		t.Error("paused menu")
	}
	m = BuildMenu(pt, MenuState{Status: Status{State: Unauthorized}})
	if find(m, CmdLink).Label != "Vincular este PC de novo" {
		t.Error("revoked token offers linking again")
	}
	m = BuildMenu(pt, MenuState{Status: Status{State: NotLinked}, LinkingCode: "ABCD-EFGH"})
	if !strings.Contains(labels(m), "Vinculando… código ABCD-EFGH|") || find(m, CmdCancelLink).ID == 0 {
		t.Error("linking menu")
	}
	// a newer version: its own entry near the top, plus the manual check
	m = BuildMenu(pt, MenuState{Status: Status{State: Running}, Username: "guto", Update: "v0.1.0-beta.10"})
	if up := find(m, CmdUpdate); up.Label != "Atualização disponível: v0.1.0-beta.10" || up.Disabled {
		t.Errorf("update entry: %+v", up)
	}
	if c := find(m, CmdCheckUpdate); c.Label != "Verificar atualizações" || c.Disabled {
		t.Errorf("check entry: %+v", c)
	}
	m = BuildMenu(Texts("en"), MenuState{Status: Status{State: NotLinked}, Checking: true})
	if find(m, CmdUpdate).ID != 0 || !find(m, CmdCheckUpdate).Disabled || find(m, CmdCheckUpdate).Label != "Checking for updates…" {
		t.Error("no update known: no entry; a running check is disabled")
	}
	m = BuildMenu(pt, MenuState{Status: Status{State: Running}, Username: "guto",
		Summaries: []Summary{{Config: config.SourceConfig{Type: "rpcs3", Path: dir}, Games: 2, Unlocks: 30}}})
	folder := findLabel(m, "RPCS3 (beta)")
	if folder.Detail != dir || folder.Note != "Acompanhando · 2 jogos, 30 troféus" || folder.Icon != "folder" {
		t.Errorf("watched folder: %+v", folder)
	}
	missing := filepath.Join(dir, "gone")
	m = BuildMenu(pt, MenuState{Status: Status{State: Running}, Summaries: []Summary{{Config: config.SourceConfig{Type: "shadps4", Path: missing}}}})
	if f := findLabel(m, "shadPS4 (beta)"); f.Note != "Pasta não encontrada" {
		t.Errorf("missing folder is shown: %+v", f)
	}
}

func findLabel(items []Item, label string) Item {
	for _, it := range items {
		if it.Label == label {
			return it
		}
		if sub := findLabel(it.Sub, label); sub.Label != "" {
			return sub
		}
	}
	return Item{}
}

func find(items []Item, id int) Item {
	for _, it := range items {
		if it.ID == id {
			return it
		}
		if sub := find(it.Sub, id); sub.ID != 0 {
			return sub
		}
	}
	return Item{}
}

// ---- detection helpers ----

func newStore(t *testing.T) *config.Store {
	t.Helper()
	t.Setenv("VIGIL_CONFIG_DIR", t.TempDir())
	store, err := config.Open()
	if err != nil {
		t.Fatal(err)
	}
	return store
}

func TestNewCandidatesAndAdd(t *testing.T) {
	store := newStore(t)
	a, b := t.TempDir(), t.TempDir()
	store.AddSource(config.SourceConfig{Type: "rpcs3", Path: a})
	found := []detect.Candidate{{Type: "rpcs3", Path: strings.ToUpper(a)}, {Type: "shadps4", Path: b}}
	fresh := NewCandidates(store, found)
	if len(fresh) != 1 || fresh[0].Type != "shadps4" {
		t.Fatalf("configured sources are not offered again: %+v", fresh)
	}
	n, err := AddCandidates(store, fresh)
	if err != nil || n != 1 || len(store.Sources()) != 2 {
		t.Fatalf("n=%d err=%v sources=%v", n, err, store.Sources())
	}
	if n, _ := AddCandidates(store, fresh); n != 0 {
		t.Fatal("adding twice is a no-op")
	}
}

func TestSafeWebURLAndLinkPage(t *testing.T) {
	for raw, ok := range map[string]bool{
		"https://vigil.example.com/link?code=X": true,
		"http://localhost:5173/link":            true,
		"http://127.0.0.1:5173/":                true,
		"http://vigil.example.com/":             false,
		"file:///C:/Windows/system32/calc.exe":  false,
		"javascript:alert(1)":                   false,
		"https://user:pw@vigil.example.com/":    false,
		"C:\\Windows\\notepad.exe":              false,
	} {
		if _, err := SafeWebURL(raw); (err == nil) != ok {
			t.Errorf("SafeWebURL(%q) ok=%v, want %v", raw, err == nil, ok)
		}
	}
	page, err := LinkPage(&api.Authorization{VerificationURIComplete: "https://v.test/link?code=ABCD-EFGH"}, "PC do quarto")
	if err != nil || page != "https://v.test/link?code=ABCD-EFGH&name=PC+do+quarto" {
		t.Fatalf("page=%q err=%v", page, err)
	}
	if _, err := LinkPage(&api.Authorization{VerificationURI: "file:///x"}, ""); err == nil {
		t.Fatal("a non-web link page is refused")
	}
}

// ---- engine ----

type fakeAPI struct {
	mu       sync.Mutex
	status   int
	received int
}

func (f *fakeAPI) handler(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.status != 0 {
		w.WriteHeader(f.status)
		return
	}
	var body struct {
		Events []map[string]any `json:"events"`
	}
	json.NewDecoder(r.Body).Decode(&body)
	f.received += len(body.Events)
	json.NewEncoder(w).Encode(map[string]any{"accepted": len(body.Events)})
}

func (f *fakeAPI) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.received
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	// Allow two actual polling intervals, including scheduling on a busy CI runner.
	deadline := time.Now().Add(2*syncer.PollInterval + time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func TestEngineStates(t *testing.T) {
	store := newStore(t)
	fake := &fakeAPI{}
	srv := httptest.NewServer(http.HandlerFunc(fake.handler))
	defer srv.Close()
	store.SetServer(srv.URL)
	changes := 0
	var mu sync.Mutex
	e := &Engine{Store: store, Log: log.New(io.Discard, "", 0), OnChange: func() { mu.Lock(); changes++; mu.Unlock() }}
	defer e.Close()

	e.Restart()
	if e.Status().State != NotLinked {
		t.Fatalf("no token: %v", e.Status().State)
	}
	store.SetToken("vgl_test")
	e.Restart()
	if e.Status().State != NoSources {
		t.Fatalf("no sources: %v", e.Status().State)
	}

	user := t.TempDir()
	line, _ := json.Marshal(map[string]any{"v": 1, "title_id": "CUSA03173", "trophy_id": 3, "unlocked_at": "2026-10-10T06:30:00Z"})
	os.WriteFile(filepath.Join(user, trophyfile.FileName), append(line, '\n'), 0o600)
	store.AddSource(config.SourceConfig{Type: "bbport", Path: user})
	e.Restart()
	if e.Status().State != Running {
		t.Fatalf("running: %v %v", e.Status().State, e.Status().Err)
	}
	waitFor(t, "the unlock to be sent", func() bool { return fake.count() == 1 })

	e.SetPaused(true)
	if st := e.Status(); !st.Paused || st.State != Stopped {
		t.Fatalf("paused: %+v", st)
	}
	e.SetPaused(false)
	if e.Status().State != Running {
		t.Fatal("resumed")
	}

	// A revoked token stops the loop and says so.
	fake.mu.Lock()
	fake.status = http.StatusUnauthorized
	fake.mu.Unlock()
	line, _ = json.Marshal(map[string]any{"v": 1, "title_id": "CUSA03173", "trophy_id": 4, "unlocked_at": "2026-10-10T06:31:00Z"})
	f, _ := os.OpenFile(filepath.Join(user, trophyfile.FileName), os.O_APPEND|os.O_WRONLY, 0o600)
	f.Write(append(line, '\n'))
	f.Close()
	waitFor(t, "unauthorized", func() bool { return e.Status().State == Unauthorized })
	mu.Lock()
	defer mu.Unlock()
	if changes == 0 {
		t.Fatal("OnChange is called")
	}
}

func TestEngineReportsLiveUnlocks(t *testing.T) {
	store := newStore(t)
	fake := &fakeAPI{}
	srv := httptest.NewServer(http.HandlerFunc(fake.handler))
	defer srv.Close()
	store.SetServer(srv.URL)
	store.SetToken("vgl_test")
	user := t.TempDir()
	path := filepath.Join(user, trophyfile.FileName)
	backlog, _ := json.Marshal(map[string]any{"v": 1, "title_id": "CUSA03173", "trophy_id": 11, "unlocked_at": "2026-10-10T06:29:00Z"})
	if err := os.WriteFile(path, append(backlog, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	store.AddSource(config.SourceConfig{Type: "bbport", Path: user})

	var mu sync.Mutex
	var got []int
	e := &Engine{Store: store, Log: log.New(io.Discard, "", 0), OnUnlocks: func(events []outbox.Event) {
		mu.Lock()
		defer mu.Unlock()
		for _, ev := range events {
			got = append(got, ev.TrophyID)
		}
	}}
	defer e.Close()
	e.Restart()
	// An accepted backlog proves the first scan completed; a sleep cannot establish that.
	waitFor(t, "the initial backlog", func() bool { return fake.count() == 1 })
	line, _ := json.Marshal(map[string]any{"v": 1, "title_id": "CUSA03173", "trophy_id": 12, "unlocked_at": "2026-10-10T06:30:00Z"})
	f, _ := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	f.Write(append(line, '\n'))
	f.Close()
	waitFor(t, "the notification", func() bool { mu.Lock(); defer mu.Unlock(); return len(got) == 1 && got[0] == 12 })
}

func TestNotificationsSubmenu(t *testing.T) {
	pt := Texts("pt-BR")
	m := BuildMenu(pt, MenuState{Status: Status{State: Running}, Username: "guto", Language: "en",
		Notify: config.NotifyPrefs{Style: config.StyleWindows, Volume: config.VolumeHigh, Corner: config.CornerBottomLeft}})
	if !find(m, CmdNotifyWindows).Checked || find(m, CmdNotifyVigil).Checked {
		t.Fatal("style checks")
	}
	if high := find(m, CmdVolumeBase+3); !high.Checked || high.Label != "Alto" || find(m, CmdVolumeBase+1).Checked {
		t.Fatalf("volume: %+v", high)
	}
	if en := find(m, CmdLangBase+1); !en.Checked || en.Label != "English" || find(m, CmdLangBase).Checked {
		t.Fatalf("language: %+v", en)
	}
	// The corner only applies to the Vigil popup.
	bl := find(m, CmdCornerBase+3)
	if bl.Label != "Canto inferior esquerdo" || !bl.Checked || !bl.Disabled {
		t.Fatalf("corner: %+v", bl)
	}
	if find(m, CmdNotifyTest).Label != "Testar notificação" {
		t.Fatal("test entry")
	}
	m = BuildMenu(pt, MenuState{Status: Status{State: Running}})
	if !find(m, CmdNotifyVigil).Checked || !find(m, CmdVolumeBase+1).Checked || !find(m, CmdCornerBase).Checked ||
		!find(m, CmdLangBase).Checked || find(m, CmdLangBase).Label != "Automático" {
		t.Fatal("defaults: Vigil style, low volume, top right, automatic language")
	}
}

func TestBuildOffers(t *testing.T) {
	user := `C:\bb\user`
	found := []detect.Candidate{
		{Type: "bbport", Path: user, Certain: true},
		{Type: "bbport-log", Path: `c:\BB\user`, Certain: true},                // same folder, other case
		{Type: "bbport-log", Path: `C:\Games\Bloodborne DUMP`, Certain: false}, // look-alike, no trophies
		{Type: "rpcs3", Path: `C:\Emu\rpcs3`, Certain: true},                   // certain but empty: kept
		{Type: "shadps4", Path: `C:\Downloads\x`, Certain: false},              // uncertain with trophies: kept
	}
	counts := map[string][2]int{"bbport": {1, 12}, "bbport-log": {1, 10}, "shadps4": {2, 5}}
	sum := func(c detect.Candidate) Summary {
		if c.Path == `C:\Games\Bloodborne DUMP` || c.Type == "rpcs3" {
			return Summary{}
		}
		n := counts[c.Type]
		return Summary{Games: n[0], Unlocks: n[1]}
	}
	offers := BuildOffers(found, sum)
	if len(offers) != 3 {
		t.Fatalf("want bbport folder, shadPS4 and rpcs3, got %+v", offers)
	}
	bb := offers[0]
	if len(bb.Candidates) != 2 || bb.Games != 1 || bb.Unlocks != 12 {
		t.Fatalf("one line per folder, counted once: %+v", bb)
	}
	pt := Texts("pt-BR")
	if got := bb.Title(pt); got != "bbport (patch) + bbport (log) — 1 jogo, 12 troféus" {
		t.Errorf("title %q", got)
	}
	if got := offers[2].Title(pt); got != "RPCS3 (beta) — 0 jogos, 0 troféus" {
		t.Errorf("empty certain source last: %q", got)
	}
	if n := len(OfferCandidates(offers)); n != 4 {
		t.Errorf("candidates to save: %d", n)
	}
}

func TestLinkErrorKey(t *testing.T) {
	for err, want := range map[error]string{
		ErrLinkDenied: "err_denied",
		fmt.Errorf("%w (invalid_grant)", ErrLinkExpired): "err_expired",
		&api.RetryableError{Status: 503}:                 "err_network",
		errors.New("disk full"):                          "err_unexpected",
	} {
		if got := LinkErrorKey(err); got != want {
			t.Errorf("%v: %s, want %s", err, got, want)
		}
		if Texts("pt-BR").T(LinkErrorKey(err)) == LinkErrorKey(err) {
			t.Errorf("%s has no text", want)
		}
	}
}

// An install made by an older version: a bbport user folder saved as shadPS4, a game dump saved as
// shadPS4 too, and a bbport log the user added by hand with its game folder. Detection now finds
// the portable port's log and recognizes the first folder as bbport.
func TestDetectionUpdateFixesOldSources(t *testing.T) {
	store := newStore(t)
	oldUser, dump, portable := `C:\old\bloodborne_pc\user`, `C:\Desktop\Bloodborne DUMP`, `C:\port\bbport-windows\user`
	game := `C:\Games\CUSA03173`
	for _, c := range []config.SourceConfig{
		{Type: "shadps4", Path: oldUser},
		{Type: "shadps4", Path: dump},
		{Type: "bbport-log", Path: portable, GameDir: game},
	} {
		store.AddSource(c)
	}
	found := []detect.Candidate{
		{Type: "bbport-log", Path: portable, GameDir: game, Certain: true}, // already configured
		{Type: "bbport-log", Path: strings.ToUpper(oldUser), GameDir: game, Certain: true},
	}
	plan := PlanDetection(store.Sources(), found)
	if len(plan.Add) != 1 || plan.Add[0].Path != strings.ToUpper(oldUser) {
		t.Fatalf("only the old folder is new: %+v", plan.Add)
	}
	if len(plan.Replace) != 1 || plan.Replace[0].Path != oldUser || plan.Replace[0].Type != "shadps4" {
		t.Fatalf("the mislabeled folder is replaced, the unknown dump is left alone: %+v", plan.Replace)
	}
	offers := MarkReplacements(BuildOffers(plan.Add, func(detect.Candidate) Summary { return Summary{} }), plan.Replace)
	if len(offers) != 1 || len(offers[0].Replaces) != 1 {
		t.Fatalf("offers: %+v", offers)
	}
	if got := offers[0].Title(Texts("pt-BR")); !strings.Contains(got, "no lugar de shadPS4") {
		t.Errorf("the dialog says what it replaces: %q", got)
	}
	added, removed, err := ApplyOffers(store, offers)
	if err != nil || added != 1 || removed != 1 {
		t.Fatalf("added=%d removed=%d err=%v", added, removed, err)
	}
	got := map[string]config.SourceConfig{}
	for _, c := range store.Sources() {
		got[c.Type+"|"+strings.ToLower(c.Path)] = c
	}
	if _, ok := got["shadps4|"+strings.ToLower(oldUser)]; ok {
		t.Fatal("the wrong type is gone")
	}
	if _, ok := got["shadps4|"+strings.ToLower(dump)]; !ok {
		t.Fatal("a folder detection does not recognize stays as the user set it")
	}
	if c := got["bbport-log|"+strings.ToLower(portable)]; c.GameDir != game {
		t.Fatalf("the hand-made source keeps its game folder (same event ids): %+v", c)
	}
	if c := got["bbport-log|"+strings.ToLower(oldUser)]; c.GameDir != game {
		t.Fatalf("the new source has the launcher's game folder: %+v", c)
	}
	if plan := PlanDetection(store.Sources(), found); len(plan.Add)+len(plan.Replace) != 0 {
		t.Fatalf("nothing left to offer: %+v", plan)
	}
}

// ---- bbport: the launcher rewrites last_run.log every session ----

type liveRecorder struct {
	mu  sync.Mutex
	ids []int
}

func (r *liveRecorder) add(events []outbox.Event) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, ev := range events {
		r.ids = append(r.ids, ev.TrophyID)
	}
}

func (r *liveRecorder) got() []int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]int(nil), r.ids...)
}

func bbportLogEngine(t *testing.T, known ...string) (*Engine, *fakeAPI, *liveRecorder, string) {
	t.Helper()
	store := newStore(t)
	fake := &fakeAPI{}
	srv := httptest.NewServer(http.HandlerFunc(fake.handler))
	t.Cleanup(srv.Close)
	store.SetServer(srv.URL)
	store.SetToken("vgl_test")
	ids := map[string]bool{}
	for _, id := range known {
		ids[id] = true
	}
	store.SetKnown(ids)
	user := t.TempDir()
	store.AddSource(config.SourceConfig{Type: "bbport-log", Path: user})
	rec := &liveRecorder{}
	e := &Engine{Store: store, Log: log.New(io.Discard, "", 0), OnUnlocks: rec.add}
	t.Cleanup(e.Close)
	return e, fake, rec, filepath.Join(user, "last_run.log")
}

func appendText(t *testing.T, path, text string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	f.WriteString(text)
	f.Close()
}

// Existing bbport-log settings pick up the persistent journal without reconfiguration.
func TestBBPortPersistentJournalHistoryAndLiveDelivery(t *testing.T) {
	e, fake, rec, runtimePath := bbportLogEngine(t)
	journal := filepath.Join(filepath.Dir(runtimePath), "trophies.log")
	if err := os.WriteFile(journal, []byte("29 1791669868\n30 1791957555\n29 1791957639\n29 1791957658\n"), 0600); err != nil {
		t.Fatal(err)
	}
	summary := Summarize(context.Background(), config.SourceConfig{Type: "bbport-log", Path: filepath.Dir(runtimePath)})
	if summary.Err != nil || summary.Games != 1 || summary.Unlocks != 2 {
		t.Fatalf("summary: %+v", summary)
	}
	e.Restart()
	waitFor(t, "journal history", func() bool { return fake.count() == 2 })
	if len(rec.got()) != 0 {
		t.Fatal("history must be silent")
	}
	appendText(t, journal, "31 1791957660\n29 1791957661\n")
	waitFor(t, "journal live delivery", func() bool { return fake.count() == 3 })
	waitFor(t, "journal notification", func() bool { got := rec.got(); return len(got) == 1 && got[0] == 31 })
	if err := os.WriteFile(runtimePath, []byte("Runtime: trophy 31 unlocked\n"), 0600); err != nil {
		t.Fatal(err)
	}
	appendText(t, journal, "32 1791957662\n")
	waitFor(t, "deduplicated delivery", func() bool { return fake.count() == 4 })
	waitFor(t, "only the new trophy notifies", func() bool { got := rec.got(); return len(got) == 2 && got[1] == 32 })
}

// A trophy line written while the agent watches is sent and announced; what an earlier session
// left in the log is sent quietly once, and the new session replacing the file loses nothing.
func TestBBPortLogLiveUnlockAfterTheLauncherRewritesTheLog(t *testing.T) {
	e, fake, rec, logPath := bbportLogEngine(t)
	os.WriteFile(logPath, []byte("Bloodborne | entry=0xa0\r\nRuntime: trophy 14 unlocked\r\n"), 0o600)
	e.Restart()
	waitFor(t, "the earlier session's trophy", func() bool { return fake.count() == 1 })

	// The launcher starts a new session: the file is rewritten (shorter), then the game unlocks.
	// (this launcher path writes "\r\r\n" line ends, as in a real log)
	os.WriteFile(logPath, []byte("Bloodborne | entry=0xa0\r\r\n"), 0o600)
	appendText(t, logPath, "Runtime: guest thread 'CSTrophy::AsyncUpdateThread' created\r\r\n")
	appendText(t, logPath, "Runtime: trophy 21 unlocked\r\r\n")
	waitFor(t, "the live trophy", func() bool { return fake.count() == 2 })
	waitFor(t, "the notification", func() bool { g := rec.got(); return len(g) == 1 && g[0] == 21 })
}

// The event id of a trophy is fixed (source, set, trophy), so an id already in known.json is
// never sent again, even when the game prints the line once more after a save was restored. To
// record an unlock again, the id must leave known.json after the game rewrote the old log.
func TestBBPortLogKnownTrophyAndTheReset(t *testing.T) {
	trophy21 := source.EventID(source.Unlock{Source: source.BBPortLog, TitleID: "CUSA03173", TrophyID: 21})

	e, fake, rec, logPath := bbportLogEngine(t, trophy21)
	os.WriteFile(logPath, []byte("Bloodborne | entry=0xa0\r\nRuntime: trophy 5 unlocked\r\n"), 0o600)
	e.Restart()
	waitFor(t, "the first scan", func() bool { return fake.count() == 1 })
	appendText(t, logPath, "Runtime: trophy 21 unlocked\r\n")
	appendText(t, logPath, "Runtime: trophy 6 unlocked\r\n")
	waitFor(t, "the next line", func() bool { return fake.count() == 2 })
	waitFor(t, "only the new trophy notifies after delivery", func() bool { g := rec.got(); return len(g) == 1 && g[0] == 6 })

	// The reset: the game was opened again (new log without 21) and only then 21 left known.json.
	e2, fake2, rec2, logPath2 := bbportLogEngine(t)
	os.WriteFile(logPath2, []byte("Bloodborne | entry=0xa0\r\nRuntime: trophy 5 unlocked\r\n"), 0o600)
	e2.Restart()
	waitFor(t, "the first scan", func() bool { return fake2.count() == 1 })
	appendText(t, logPath2, "Runtime: trophy 21 unlocked\r\n")
	waitFor(t, "the recorded unlock", func() bool { g := rec2.got(); return len(g) == 1 && g[0] == 21 })
}
