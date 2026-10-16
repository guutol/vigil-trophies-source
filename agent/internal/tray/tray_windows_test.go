//go:build windows

package tray

import (
	"os"
	"path/filepath"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/vigil-tracker/agent/internal/app"
)

func TestMenuTextEscapesAmpersand(t *testing.T) {
	if got := menuText("Ratchet & Clank"); got != "Ratchet && Clank" {
		t.Fatalf("got %q", got)
	}
}

func TestCopyUTF16TruncatesAndTerminates(t *testing.T) {
	buf := make([]uint16, 8)
	copyUTF16(buf, "Troféu desbloqueado")
	if buf[len(buf)-1] != 0 {
		t.Fatal("must stay NUL-terminated")
	}
	if got := windows.UTF16ToString(buf); got != "Troféu " {
		t.Fatalf("got %q", got)
	}
	small := make([]uint16, 64)
	copyUTF16(small, "ok")
	if windows.UTF16ToString(small) != "ok" {
		t.Fatal("short text copied whole")
	}
}

// The real menu, built from the same model the app uses (no window is shown).
func TestBuildMenuFromTheModel(t *testing.T) {
	pGetMenuItemCount := user32.NewProc("GetMenuItemCount")
	items := app.BuildMenu(app.Texts("pt-BR"), app.MenuState{Status: app.Status{State: app.NotLinked}})
	h := buildMenu(items)
	if h == 0 {
		t.Fatal("no menu")
	}
	defer pDestroyMenu.Call(h)
	n, _, _ := pGetMenuItemCount.Call(h)
	if int(n) != len(items) {
		t.Fatalf("menu has %d entries, model %d", n, len(items))
	}
}

func TestNotifyIconDataSize(t *testing.T) {
	// NOTIFYICONDATAW (Vista and later) is 976 bytes on 64-bit Windows.
	if got := unsafe.Sizeof(notifyIconData{}); got != 976 {
		t.Fatalf("NOTIFYICONDATAW size %d, want 976", got)
	}
}

func TestOpenRefusesWhatCouldRun(t *testing.T) {
	for _, u := range []string{"file:///C:/Windows/System32/calc.exe", "C:\\Windows\\notepad.exe", "http://evil.example/"} {
		if err := OpenURL(u); err == nil {
			t.Errorf("OpenURL(%q) should be refused", u)
		}
	}
	exe := filepath.Join(t.TempDir(), "x.exe")
	os.WriteFile(exe, []byte("MZ"), 0o600)
	if err := OpenPath(exe); err == nil {
		t.Error("an .exe must never be opened")
	}
	if err := OpenPath(filepath.Join(t.TempDir(), "missing.log")); err == nil {
		t.Error("a missing file is an error")
	}
}
