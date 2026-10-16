package outbox

import (
	"os"
	"path/filepath"
	"testing"
)

func TestOutboxSurvivesReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "outbox.jsonl")
	o, err := Open(path)
	if err != nil || o.Len() != 0 {
		t.Fatalf("new outbox: %v %d", err, o.Len())
	}
	if err := o.Add(
		Event{EventID: "a", TitleID: "CUSA03173", TrophyID: 1, UnlockedAt: "2026-10-10T06:30:00Z"},
		Event{EventID: "b", TitleID: "CUSA03173", TrophyID: 2, UnlockedAt: "2026-10-10T06:30:00Z"},
		Event{EventID: "c", TitleID: "CUSA03173", TrophyID: 3, UnlockedAt: "2026-10-10T06:30:00Z"},
	); err != nil {
		t.Fatal(err)
	}

	again, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	got := again.Peek(10)
	if len(got) != 3 || got[0].EventID != "a" || got[2].TrophyID != 3 || len(again.IDs()) != 3 {
		t.Fatalf("reopened outbox differs: %+v", got)
	}

	if err := again.Remove("a", "c"); err != nil {
		t.Fatal(err)
	}
	third, _ := Open(path)
	if third.Len() != 1 || third.Peek(1)[0].EventID != "b" {
		t.Fatalf("remove not persisted: %+v", third.Peek(5))
	}
}

func TestPeekIsBounded(t *testing.T) {
	o, _ := Open(filepath.Join(t.TempDir(), "outbox.jsonl"))
	for i := 0; i < 5; i++ {
		o.Add(Event{EventID: string(rune('a' + i))})
	}
	if len(o.Peek(2)) != 2 || len(o.Peek(99)) != 5 {
		t.Fatal("peek bounds")
	}
}

func TestOldAgentOutboxLinesStillLoad(t *testing.T) {
	path := filepath.Join(t.TempDir(), "outbox.jsonl")
	old := `{"event_id":"a","title_id":"CUSA03173","trophy_id":1,"unlocked_at":"2026-10-10T06:30:00Z","source_end":83}` + "\n" +
		"garbage\n"
	if err := os.WriteFile(path, []byte(old), 0o600); err != nil {
		t.Fatal(err)
	}
	o, err := Open(path)
	if err != nil || o.Len() != 1 || o.Peek(1)[0].TitleID != "CUSA03173" || o.Peek(1)[0].Source != "" {
		t.Fatalf("old line: %v %+v", err, o.Peek(1))
	}
}
