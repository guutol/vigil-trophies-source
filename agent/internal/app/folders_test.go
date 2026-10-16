package app

import (
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/vigil-tracker/agent/internal/config"
	"github.com/vigil-tracker/agent/internal/outbox"
	"github.com/vigil-tracker/agent/internal/source/detect"
)

func TestFoldersGroupTypesOfOneFolder(t *testing.T) {
	dir := t.TempDir()
	other := t.TempDir()
	equivalent := filepath.Join(dir, ".") + string(os.PathSeparator)
	if runtime.GOOS == "windows" {
		equivalent = strings.ToUpper(equivalent)
	}
	sums := []Summary{
		{Config: config.SourceConfig{Type: "bbport-log", Path: dir}, Games: 1, Unlocks: 12},
		{Config: config.SourceConfig{Type: "rpcs3", Path: other}, Games: 2, Unlocks: 30},
		{Config: config.SourceConfig{Type: "bbport", Path: equivalent}, Games: 1, Unlocks: 3},
	}
	fs := Folders(sums)
	if len(fs) != 2 || len(fs[0].Sources) != 2 || fs[1].Path != other {
		t.Fatalf("folders: %+v", fs)
	}
	pt := Texts("pt-BR")
	if got := fs[0].Types(pt); got != "bbport (log) + bbport (patch)" {
		t.Errorf("types: %q", got)
	}
	if got := fs[0].Status(pt, Status{State: Running}); got != "Acompanhando" {
		t.Errorf("status: %q", got)
	}
	if got := fs[0].Status(pt, Status{State: Stopped, Paused: true}); !strings.HasPrefix(got, "Sincronização pausada") {
		t.Errorf("paused: %q", got)
	}
	if got := fs[0].Status(pt, Status{State: NotLinked}); !strings.HasPrefix(got, "Aguardando o vínculo") {
		t.Errorf("not linked: %q", got)
	}
	gone := Folder{Path: filepath.Join(dir, "gone"), Sources: sums[:1]}
	if got := gone.Status(pt, Status{State: Running}); got != "Pasta não encontrada" {
		t.Errorf("missing: %q", got)
	}
}

func TestManageFoldersMenu(t *testing.T) {
	a, b := t.TempDir(), t.TempDir()
	stopped := `C:\Games\Old\user`
	m := BuildMenu(Texts("pt-BR"), MenuState{Status: Status{State: Running}, Username: "guto",
		Summaries: []Summary{
			{Config: config.SourceConfig{Type: "rpcs3", Path: a}},
			{Config: config.SourceConfig{Type: "shadps4", Path: b}},
		},
		Stopped: []config.StoppedFolder{{Path: stopped, Sources: []config.SourceConfig{{Type: "bbport-log", Path: stopped}}}}})
	manage := findLabel(m, "Gerenciar pastas")
	if manage.Icon != "folder" || len(manage.Sub) == 0 {
		t.Fatalf("manage folders: %+v", manage)
	}
	// each folder: open it, or stop watching it (the removal is marked as such)
	second := findLabel(manage.Sub, "shadPS4 (beta)")
	if second.Detail != b || len(second.Sub) != 2 || second.Sub[0].ID != CmdFolderOpenBase+1 ||
		second.Sub[1].ID != CmdFolderStopBase+1 || !second.Sub[1].Danger || second.Sub[1].Label != "Parar de acompanhar" {
		t.Fatalf("second folder: %+v", second)
	}
	// folders found or added another way stay available
	if find(manage.Sub, CmdDetect).Label != "Procurar pastas de novo" || find(manage.Sub, CmdAddFolder).ID == 0 {
		t.Error("detect and add are still there")
	}
	// the way back for a stopped folder
	back := findLabel(manage.Sub, "Pastas que você parou de acompanhar (1)")
	if len(back.Sub) != 1 || back.Sub[0].ID != CmdFolderRestoreBase || back.Sub[0].Detail != stopped ||
		back.Sub[0].Label != "Voltar a acompanhar" || back.Sub[0].Note != "bbport (log)" {
		t.Fatalf("stopped folders: %+v", back)
	}
	// nothing watched: say so, and keep the ways to add
	m = BuildMenu(Texts("en"), MenuState{Status: Status{State: NoSources}, Username: "guto"})
	manage = findLabel(m, "Manage folders")
	if manage.Sub[0].Label != "No watched folders yet" || !manage.Sub[0].Disabled || find(manage.Sub, CmdAddFolder).ID == 0 {
		t.Fatalf("empty: %+v", manage.Sub)
	}
	if findLabel(m, "Folders you stopped watching (0)").Label != "" {
		t.Error("no stopped list when there is none")
	}
}

func TestDetectionSkipsStoppedFolders(t *testing.T) {
	bb, rpc := t.TempDir(), t.TempDir()
	found := []detect.Candidate{
		{Type: "bbport-log", Path: bb},
		{Type: "bbport", Path: bb},
		{Type: "rpcs3", Path: rpc},
	}
	stopped := []config.StoppedFolder{{Path: filepath.Join(bb, ".")}}
	got := WithoutStopped(found, stopped)
	if len(got) != 1 || got[0].Type != "rpcs3" {
		t.Fatalf("stopped folder offered again: %+v", got)
	}
	// and the plan built from it neither adds nor replaces anything there
	plan := PlanDetection(nil, got)
	for _, c := range plan.Add {
		if config.SameFolder(c.Path, bb) {
			t.Fatal("planned to add a stopped folder")
		}
	}
}

// Removing the last folder: nothing is watched any more, but trophies already queued still go out.
func TestRemovingTheLastFolderStillSendsTheQueue(t *testing.T) {
	store := newStore(t)
	fake := &fakeAPI{}
	srv := httptest.NewServer(http.HandlerFunc(fake.handler))
	defer srv.Close()
	store.SetServer(srv.URL)
	store.SetToken("vgl_test")
	user := t.TempDir()
	game := filepath.Join(user, "last_run.log")
	os.WriteFile(game, []byte("Bloodborne | entry=0xa0\r\n"), 0o600)
	store.AddSource(config.SourceConfig{Type: "bbport-log", Path: user})

	// a trophy found earlier waits in the queue (the server was offline)
	ob, err := outbox.Open(store.OutboxPath())
	if err != nil {
		t.Fatal(err)
	}
	if err := ob.Add(outbox.Event{EventID: "11111111-1111-5111-8111-111111111111", Source: "bbport-log",
		TitleID: "CUSA03173", TrophyID: 9, UnlockedAt: "2026-10-15T01:00:00Z"}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.StopWatching(user, time.Now()); err != nil {
		t.Fatal(err)
	}
	e := &Engine{Store: store, Log: log.New(io.Discard, "", 0)}
	defer e.Close()
	e.Restart()
	if e.Status().State != Running {
		t.Fatalf("a queued trophy keeps the loop running: %v", e.Status().State)
	}
	waitFor(t, "the queued trophy", func() bool { return fake.count() == 1 })
	waitFor(t, "an empty queue", func() bool { return queued(store) == 0 })
	if b, _ := os.ReadFile(game); string(b) != "Bloodborne | entry=0xa0\r\n" {
		t.Error("the game's log changed")
	}
	// nothing queued and nothing watched: the app says there is nothing to watch
	e.Restart()
	if e.Status().State != NoSources {
		t.Fatalf("after the queue: %v", e.Status().State)
	}
}
