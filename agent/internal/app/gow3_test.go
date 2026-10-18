package app

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/vigil-tracker/agent/internal/config"
	"github.com/vigil-tracker/agent/internal/source"
	"github.com/vigil-tracker/agent/internal/source/bbportlog"
	"github.com/vigil-tracker/agent/internal/source/detect"
)

// Made-up set id for the tests (NPWR9xxxx), not a catalog id.
const testGOW3Set = "NPWR90301_00"

// isolateLaunchers points the launchers' settings at empty folders, so a God of War III launcher
// installed on the machine running the tests is never read.
func isolateLaunchers(t *testing.T) {
	t.Helper()
	empty := t.TempDir()
	for _, k := range []string{"APPDATA", "LOCALAPPDATA", "XDG_CONFIG_HOME", "XDG_DATA_HOME", "HOME", "USERPROFILE"} {
		t.Setenv(k, empty)
	}
	t.Setenv("GOW3_DATA_DIR", "")
	t.Setenv("SOTC_GAME_DIR", "")
	t.Setenv("SOTC_USER_DIR", "")
}

// gow3Dump writes a synthetic game folder: param.sfo (title id, title) and npbind.dat (the set).
func gow3Dump(t *testing.T, titleID, set string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), titleID)
	os.MkdirAll(filepath.Join(dir, "sce_sys"), 0o700)
	os.WriteFile(filepath.Join(dir, "sce_sys", "param.sfo"), testSFO(map[string]string{"TITLE_ID": titleID, "TITLE": "God of War III Remastered"}), 0o600)
	os.WriteFile(filepath.Join(dir, "sce_sys", "npbind.dat"), []byte("\x00\x02"+set+"\x00rest"), 0o600)
	return dir
}

func testSFO(entries map[string]string) []byte {
	keys := make([]string, 0, len(entries))
	for k := range entries {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var keyTable, dataTable, index []byte
	for _, k := range keys {
		v := append([]byte(entries[k]), 0)
		e := make([]byte, 16)
		binary.LittleEndian.PutUint16(e[0:], uint16(len(keyTable)))
		binary.LittleEndian.PutUint16(e[2:], 0x0204)
		binary.LittleEndian.PutUint32(e[4:], uint32(len(v)))
		binary.LittleEndian.PutUint32(e[8:], uint32(len(v)))
		binary.LittleEndian.PutUint32(e[12:], uint32(len(dataTable)))
		index = append(index, e...)
		keyTable = append(keyTable, append([]byte(k), 0)...)
		dataTable = append(dataTable, v...)
	}
	head := make([]byte, 20)
	copy(head, "\x00PSF")
	binary.LittleEndian.PutUint32(head[4:], 0x101)
	binary.LittleEndian.PutUint32(head[8:], uint32(20+len(index)))
	binary.LittleEndian.PutUint32(head[12:], uint32(20+len(index)+len(keyTable)))
	binary.LittleEndian.PutUint32(head[16:], uint32(len(keys)))
	return append(append(append(head, index...), keyTable...), dataTable...)
}

// bodyAPI is the server side of the test: it keeps every event it accepts, and can refuse
// (offline) until told otherwise.
type bodyAPI struct {
	mu      sync.Mutex
	offline bool
	events  []map[string]any
}

func (f *bodyAPI) handler(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.offline {
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	var body struct {
		Events []map[string]any `json:"events"`
	}
	json.NewDecoder(r.Body).Decode(&body)
	f.events = append(f.events, body.Events...)
	json.NewEncoder(w).Encode(map[string]any{"accepted": len(body.Events)})
}

func (f *bodyAPI) setOffline(v bool) { f.mu.Lock(); f.offline = v; f.mu.Unlock() }

func (f *bodyAPI) got() []map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]map[string]any(nil), f.events...)
}

func gow3Engine(t *testing.T, sources ...config.SourceConfig) (*Engine, *bodyAPI, *liveRecorder) {
	t.Helper()
	isolateLaunchers(t)
	store := newStore(t)
	fake := &bodyAPI{}
	srv := httptest.NewServer(http.HandlerFunc(fake.handler))
	t.Cleanup(srv.Close)
	store.SetServer(srv.URL)
	store.SetToken("vgl_test")
	for _, c := range sources {
		if _, err := store.AddSource(c); err != nil {
			t.Fatal(err)
		}
	}
	rec := &liveRecorder{}
	e := &Engine{Store: store, Log: log.New(io.Discard, "", 0), OnUnlocks: rec.add}
	t.Cleanup(e.Close)
	return e, fake, rec
}

// The whole path for the port: the launcher's log -> the engine -> the API (source gow3-log, the
// set read from the dump) -> the notification; offline, the unlock waits in the queue.
func TestGOW3LogEngineToAPIToNotification(t *testing.T) {
	user := t.TempDir()
	game := gow3Dump(t, "CUSA01623", testGOW3Set)
	e, fake, rec := gow3Engine(t, config.SourceConfig{Type: source.GOW3Log, Path: user, GameDir: game})
	logPath := filepath.Join(user, bbportlog.LogName)

	// an earlier session's log: sent quietly
	os.WriteFile(logPath, []byte("GOW3 runtime\r\r\nRuntime: trophy 2 unlocked\r\r\n"), 0o600)
	e.Restart()
	waitFor(t, "the earlier session's trophy", func() bool { return len(fake.got()) == 1 })
	if len(rec.got()) != 0 {
		t.Fatal("history must be silent")
	}
	ev := fake.got()[0]
	if ev["source"] != source.GOW3Log || ev["trophy_set_id"] != testGOW3Set || ev["title_id"] != "CUSA01623" ||
		ev["trophy_id"] != float64(2) || ev["unlocked_at"] == "" {
		t.Fatalf("event sent: %+v", ev)
	}

	// offline: a new session (the launcher rewrites the log) unlocks a trophy; it waits in the queue
	fake.setOffline(true)
	os.WriteFile(logPath, []byte("GOW3 runtime\r\r\n"), 0o600)
	appendText(t, logPath, "Runtime: trophy 9 unlocked\r\r\n")
	time.Sleep(500 * time.Millisecond)
	if len(fake.got()) != 1 {
		t.Fatal("nothing reaches an offline server")
	}
	fake.setOffline(false)
	waitFor(t, "the queued trophy", func() bool { return len(fake.got()) == 2 })
	waitFor(t, "the notification", func() bool { g := rec.got(); return len(g) == 1 && g[0] == 9 })
	if ev := fake.got()[1]; ev["source"] != source.GOW3Log || ev["trophy_set_id"] != testGOW3Set {
		t.Fatalf("queued event: %+v", ev)
	}
	// the same line printed again (a restored save) is not sent twice
	appendText(t, logPath, "Runtime: trophy 9 unlocked\r\r\n")
	time.Sleep(500 * time.Millisecond)
	if n := len(fake.got()); n != 2 {
		t.Fatalf("duplicate sent: %d events", n)
	}
}

// Without an identified game nothing is sent (and never as Bloodborne); the folder says why.
func TestUnidentifiedGOW3SendsNothing(t *testing.T) {
	user := t.TempDir()
	os.WriteFile(filepath.Join(user, bbportlog.LogName), []byte("Runtime: trophy 4 unlocked\n"), 0o600)
	c := config.SourceConfig{Type: source.GOW3Log, Path: user}
	e, fake, _ := gow3Engine(t, c)
	e.Restart()
	time.Sleep(time.Second)
	if n := len(fake.got()); n != 0 {
		t.Fatalf("an unidentified port sent %d events: %+v", n, fake.got())
	}
	sum := Summarize(context.Background(), c)
	if !bbportlog.IsUnidentified(sum.Err) {
		t.Fatalf("summary: %+v", sum)
	}
	for _, lang := range []string{"en", "pt-BR"} {
		tx := Texts(lang)
		got := Folders([]Summary{sum})[0].Status(tx, Status{State: Running})
		if got != tx.T("folder_status_gow3_unidentified") || got == tx.T("folder_status_error") {
			t.Errorf("%s status: %q", lang, got)
		}
	}
}

// The saved game folder wins; when it no longer identifies the game, the launcher's current one
// is used (the player moved the dump and chose it again in the launcher).
func TestIdentifyGOW3FallsBackToTheLauncherSettings(t *testing.T) {
	isolateLaunchers(t)
	game := gow3Dump(t, "CUSA01623", testGOW3Set)
	settings := filepath.Join(os.Getenv("APPDATA"), "gow3-launcher", "settings.json")
	if detect.GOW3SettingsPath(detect.FromOSLight()) != settings {
		t.Skipf("launcher settings path on this OS: %s", detect.GOW3SettingsPath(detect.FromOSLight()))
	}
	os.MkdirAll(filepath.Dir(settings), 0o700)
	raw, _ := json.Marshal(map[string]string{"game_dir": game, "update_dir": "", "user_dir": ""})
	os.WriteFile(settings, raw, 0o600)
	g, err := IdentifyGOW3(config.SourceConfig{Type: source.GOW3Log, Path: t.TempDir(), GameDir: filepath.Join(t.TempDir(), "moved")})
	if err != nil || g.TrophySetID != testGOW3Set {
		t.Fatalf("launcher's game folder: %+v %v", g, err)
	}
}

// Bloodborne and God of War III at once: each event goes with its own source and set.
func TestGOW3AndBloodborneTogether(t *testing.T) {
	gowUser, bbUser := t.TempDir(), t.TempDir()
	game := gow3Dump(t, "CUSA01623", testGOW3Set)
	e, fake, _ := gow3Engine(t,
		config.SourceConfig{Type: source.GOW3Log, Path: gowUser, GameDir: game},
		config.SourceConfig{Type: source.BBPortLog, Path: bbUser})
	os.WriteFile(filepath.Join(gowUser, bbportlog.LogName), []byte("Runtime: trophy 5 unlocked\n"), 0o600)
	os.WriteFile(filepath.Join(bbUser, bbportlog.LogName), []byte("Runtime: trophy 5 unlocked\n"), 0o600)
	e.Restart()
	waitFor(t, "both trophies", func() bool { return len(fake.got()) == 2 })
	bySource := map[any]map[string]any{}
	for _, ev := range fake.got() {
		bySource[ev["source"]] = ev
	}
	if bySource[source.GOW3Log]["trophy_set_id"] != testGOW3Set || bySource[source.BBPortLog]["title_id"] != "CUSA03173" ||
		bySource[source.BBPortLog]["trophy_set_id"] == testGOW3Set {
		t.Fatalf("events: %+v", fake.got())
	}
}

// A port folder the player stopped watching is not offered again by detection.
func TestStoppedGOW3FolderIsNotOfferedAgain(t *testing.T) {
	isolateLaunchers(t)
	store := newStore(t)
	user := t.TempDir()
	store.AddSource(config.SourceConfig{Type: source.GOW3Log, Path: user})
	if _, err := store.StopWatching(user, time.Now()); err != nil {
		t.Fatal(err)
	}
	found := []detect.Candidate{{Type: source.GOW3Log, Path: user, Certain: true}}
	if got := WithoutStopped(found, store.StoppedFolders()); len(got) != 0 {
		t.Fatalf("stopped folder offered again: %+v", got)
	}
}

// A God of War III saves folder an older agent took for bbport-log is offered again as the port's,
// replacing the wrong source (after the player accepts).
func TestMislabeledGOW3FolderIsReplaced(t *testing.T) {
	user := t.TempDir()
	configured := []config.SourceConfig{{Type: source.BBPortLog, Path: user}}
	found := []detect.Candidate{{Type: source.GOW3Log, Path: user, Certain: true}}
	plan := PlanDetection(configured, found)
	if len(plan.Add) != 1 || plan.Add[0].Type != source.GOW3Log || len(plan.Replace) != 1 || plan.Replace[0].Type != source.BBPortLog {
		t.Fatalf("plan: %+v", plan)
	}
}
