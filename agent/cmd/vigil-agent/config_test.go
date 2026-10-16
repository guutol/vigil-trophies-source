package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/vigil-tracker/agent/internal/config"
)

func testStore(t *testing.T) *config.Store {
	t.Helper()
	t.Setenv("VIGIL_CONFIG_DIR", t.TempDir())
	store, err := config.Open()
	if err != nil {
		t.Fatal(err)
	}
	return store
}

func TestConfigServerSavesAnHTTPSServerAndForgetsTheOldLink(t *testing.T) {
	store := testStore(t)
	if err := store.SetToken("old-server-token"); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := configure(store, []string{"server", "https://vigil.example.test/"}, &out); err != nil {
		t.Fatal(err)
	}
	if got := store.Server(); got != "https://vigil.example.test" {
		t.Fatalf("server = %q", got)
	}
	if token, _ := store.Token(); token != "" {
		t.Fatal("the token of the previous server must not be kept for another host")
	}
	if !strings.Contains(out.String(), "link this PC again") {
		t.Fatalf("should say to link again: %q", out.String())
	}
}

func TestConfigServerRefusesPlainHTTPAndGarbage(t *testing.T) {
	store := testStore(t)
	before := store.Server()
	for _, bad := range []string{"http://vigil.example.test", "ftp://x", "not a url", ""} {
		if err := configure(store, []string{"server", bad}, &bytes.Buffer{}); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
	if store.Server() != before {
		t.Fatal("a refused value must not change the server")
	}
	// local development still works
	if err := configure(store, []string{"server", "http://localhost:8080"}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
}

func TestConfigUsage(t *testing.T) {
	store := testStore(t)
	if err := configure(store, []string{"server"}, &bytes.Buffer{}); err == nil {
		t.Fatal("missing value accepted")
	}
	if err := configure(store, []string{"catalog-proposals", "off"}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
}
