package bbport

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/vigil-tracker/agent/internal/source"
	"github.com/vigil-tracker/agent/internal/trophyfile"
)

func appendTo(t *testing.T, dir, data string) {
	t.Helper()
	f, err := os.OpenFile(filepath.Join(dir, trophyfile.FileName), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	f.WriteString(data)
}

const l1 = `{"v":1,"title_id":"CUSA03173","trophy_id":1,"unlocked_at":"2026-10-10T06:30:00Z"}` + "\n"
const l2 = `{"v":1,"title_id":"CUSA03173","trophy_id":2,"unlocked_at":"2026-10-10T06:31:00Z"}` + "\n"

func TestScanReadsAppendedLines(t *testing.T) {
	dir := t.TempDir()
	s, _ := New(dir)
	if got, err := s.Scan(context.Background()); err != nil || len(got) != 0 {
		t.Fatalf("missing file: %v %v", got, err)
	}
	var invalid int
	s.OnInvalid = func(error) { invalid++ }
	appendTo(t, dir, l1+"not json\n"+l1) // repeated line collapses into one unlock
	got, _ := s.Scan(context.Background())
	if len(got) != 1 || invalid != 1 {
		t.Fatalf("got %+v invalid=%d", got, invalid)
	}
	u := got[0]
	if u.Source != source.BBPort || u.TitleID != "CUSA03173" || u.TrophySetID != "" || u.TrophyID != 1 ||
		u.UnlockedAt != "2026-10-10T06:30:00Z" {
		t.Fatalf("unlock %+v", u)
	}

	appendTo(t, dir, l2[:30]) // half a line: not read yet
	if got, _ := s.Scan(context.Background()); len(got) != 1 {
		t.Fatalf("partial line read: %+v", got)
	}
	appendTo(t, dir, l2[30:])
	if got, _ := s.Scan(context.Background()); len(got) != 2 || got[1].TrophyID != 2 {
		t.Fatalf("completed line: %+v", got)
	}
}

func TestTruncatedFileIsReadAgain(t *testing.T) {
	dir := t.TempDir()
	s, _ := New(dir)
	appendTo(t, dir, l1+l2)
	s.Scan(context.Background())
	os.WriteFile(filepath.Join(dir, trophyfile.FileName), []byte(l2), 0o600)
	got, err := s.Scan(context.Background())
	if err != nil || len(got) != 2 {
		t.Fatalf("after truncation: %+v %v", got, err)
	}
}
