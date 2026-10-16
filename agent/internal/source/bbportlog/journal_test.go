package bbportlog

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/vigil-tracker/agent/internal/source"
)

func TestPersistentJournalAndRuntimeShareIdentity(t *testing.T) {
	dir := t.TempDir()
	journal := filepath.Join(dir, TrophyLogName)
	write := func(path, data string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	write(journal, "29 1791669868\r\n30 1791957555\r\n29 1791957639\n29 1791957658\ninvalid\n31 1791957")
	s, err := ForUserDir(dir, Game{})
	if err != nil {
		t.Fatal(err)
	}
	scan := func(want int) []source.Unlock {
		t.Helper()
		got, err := s.Scan(context.Background())
		if err != nil || len(got) != want {
			t.Fatalf("scan: %+v, %v; want %d", got, err, want)
		}
		return got
	}
	got := scan(2)
	if got[0].TrophyID != 29 || got[1].TrophyID != 30 || got[0].UnlockedAt != source.FormatTime(time.Unix(1791669868, 0)) {
		t.Fatalf("unexpected journal: %+v", got)
	}
	write(filepath.Join(dir, LogName), "Runtime: trophy 29 unlocked\r\r\n")
	scan(2)
	// Completing the partial line produces exactly one new trophy.
	f, err := os.OpenFile(journal, os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	f.WriteString("555\n")
	f.Close()
	scan(3)
	// Truncation/replacement must not lose known unlocks or repeat them.
	write(journal, "32 1791957555\n")
	scan(4)
	restarted, _ := ForUserDir(dir, Game{})
	fresh, err := restarted.Scan(context.Background())
	if err != nil || len(fresh) != 2 {
		t.Fatalf("restart: %+v %v", fresh, err)
	}
	if source.EventID(fresh[1]) != source.EventID(got[0]) {
		t.Fatal("legacy runtime identity changed")
	}
}

func TestJournalRejectsMalformedRecords(t *testing.T) {
	for _, line := range []string{"", "29", "29 1791957555 extra", "-1 1791957555", "128 1791957555", "29 0", "29 99999999999999999999", "x 1791957555"} {
		if _, _, ok := ParseTrophyLine(line); ok {
			t.Fatalf("accepted %q", line)
		}
	}
}
