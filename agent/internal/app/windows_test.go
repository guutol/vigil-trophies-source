//go:build windows

package app

import (
	"testing"

	"golang.org/x/sys/windows/registry"
)

// The real registry, under a throwaway key (never the real Run key).
func TestRegistryRunKey(t *testing.T) {
	const path = `Software\VigilTrophiesTest`
	t.Cleanup(func() { registry.DeleteKey(registry.CURRENT_USER, path) })
	a := Autostart{Key: RegistryRunKey{Path: path}, Exe: `C:\Vigil Test\vigil-agent-tray.exe`}
	if on, err := a.Enabled(); on || err != nil {
		t.Fatalf("missing key: on=%v err=%v", on, err)
	}
	if err := a.SetEnabled(true); err != nil {
		t.Fatal(err)
	}
	if on, err := a.Enabled(); !on || err != nil {
		t.Fatalf("on=%v err=%v", on, err)
	}
	if err := a.SetEnabled(false); err != nil {
		t.Fatal(err)
	}
	if err := a.SetEnabled(false); err != nil {
		t.Fatal("removing twice is fine")
	}
	if on, _ := a.Enabled(); on {
		t.Fatal("still on")
	}
}

func TestSingleInstance(t *testing.T) {
	name := "VigilTrophiesTest-" + t.Name()
	release, first, err := SingleInstance(name)
	if err != nil || !first {
		t.Fatalf("first copy: first=%v err=%v", first, err)
	}
	_, second, err := SingleInstance(name)
	if err != nil || second {
		t.Fatalf("second copy must see the first: first=%v err=%v", second, err)
	}
	release()
	release2, again, err := SingleInstance(name)
	if err != nil || !again {
		t.Fatalf("after quitting, a new copy is first: %v %v", again, err)
	}
	release2()
}

func TestUserLocale(t *testing.T) {
	if UserLocale() == "" {
		t.Fatal("empty locale")
	}
}
