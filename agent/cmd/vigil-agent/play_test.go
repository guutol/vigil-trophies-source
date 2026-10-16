package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/vigil-tracker/agent/internal/config"
)

// The test binary doubles as a fake game: with VIGIL_FAKE_GAME=1 it prints what the official
// bbport prints and exits. Nothing here touches real game files.
func TestMain(m *testing.M) {
	if os.Getenv("VIGIL_FAKE_GAME") == "1" {
		fakeGame()
		return
	}
	os.Exit(m.Run())
}

func fakeGame() {
	// the arguments arrive untouched: no shell expanded or split them
	fmt.Printf("args: %q\n", os.Args[len(os.Args)-2:])
	fmt.Println("Bloodborne | entry=0xa0")
	fmt.Println("Runtime: trophy 7 unlocked")
	fmt.Fprintln(os.Stderr, "Runtime: trophy 300 unlocked") // out of range: ignored
	fmt.Println("Runtime: trophy 8 unlocked!")              // not the exact line: ignored
	time.Sleep(300 * time.Millisecond)
	os.Exit(3)
}

func TestPlayRunsTheGameAndUploadsItsTrophies(t *testing.T) {
	var mu sync.Mutex
	var events []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Events []map[string]any `json:"events"`
		}
		json.NewDecoder(r.Body).Decode(&body)
		mu.Lock()
		events = append(events, body.Events...)
		mu.Unlock()
		json.NewEncoder(w).Encode(map[string]any{"accepted": len(body.Events), "duplicates": 0, "pending": 0,
			"rejected": []any{}})
	}))
	defer srv.Close()

	t.Setenv("VIGIL_CONFIG_DIR", t.TempDir())
	t.Setenv("VIGIL_FAKE_GAME", "1")
	store, err := config.Open()
	if err != nil {
		t.Fatal(err)
	}
	store.SetServer(srv.URL)
	store.SetToken("vgl_test")

	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	var logs strings.Builder
	logger := log.New(&logs, "", 0)
	stdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w // play echoes the game's output
	done := make(chan string)
	go func() {
		b, _ := io.ReadAll(r)
		done <- string(b)
	}()
	err = play(context.Background(), store,
		[]string{"bbport", "--exit-with-game", "--", exe, "-test.run=^$", "a b; echo $HOME", "&& del *"}, logger)
	w.Close()
	os.Stdout = stdout
	echoed := <-done
	if err != nil {
		t.Fatalf("play: %v\n%s", err, logs.String())
	}

	if !strings.Contains(echoed, `"a b; echo $HOME" "&& del *"`) {
		t.Fatalf("arguments were not passed literally; echoed:\n%s", echoed)
	}
	if !strings.Contains(echoed, "Runtime: trophy 7 unlocked") {
		t.Fatalf("game output not shown:\n%s", echoed)
	}
	if !strings.Contains(logs.String(), "the game exited (code 3)") {
		t.Fatalf("exit not reported:\n%s", logs.String())
	}
	mu.Lock()
	defer mu.Unlock()
	if len(events) != 1 || events[0]["source"] != "bbport-log" || events[0]["trophy_id"] != float64(7) ||
		events[0]["title_id"] != "CUSA03173" || events[0]["unlocked_at"] == "" {
		t.Fatalf("sent %v", events)
	}
	// The output stays in the agent's own folder, never in the port's.
	data, err := os.ReadFile(playLogPath(store))
	if err != nil || !strings.Contains(string(data), "Runtime: trophy 7 unlocked") {
		t.Fatalf("play log: %v %q", err, data)
	}
}

func TestPlayNeedsACommand(t *testing.T) {
	t.Setenv("VIGIL_CONFIG_DIR", t.TempDir())
	store, _ := config.Open()
	logger := log.New(io.Discard, "", 0)
	for _, args := range [][]string{nil, {"rpcs3"}, {"bbport"}, {"bbport", "--"}} {
		if err := play(context.Background(), store, args, logger); err == nil {
			t.Errorf("%v: accepted", args)
		}
	}
}
