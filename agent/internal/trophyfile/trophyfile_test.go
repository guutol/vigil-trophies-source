package trophyfile

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

const validLine = `{"v":1,"title_id":"CUSA03173","trophy_id":12,"unlocked_at":"2026-10-10T06:30:00Z"}`

func TestParseLineValid(t *testing.T) {
	u, err := ParseLine([]byte(validLine))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if u.TitleID != "CUSA03173" || u.TrophyID != 12 || u.UnlockedAt != "2026-10-10T06:30:00Z" {
		t.Fatalf("wrong parse: %+v", u)
	}
	// unknown fields, CRLF and other field order are fine
	if _, err := ParseLine([]byte(`{"trophy_id":0,"v":1,"extra":true,"unlocked_at":"2026-10-10T06:30:00Z","title_id":"ABCD"}` + "\r")); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestParseLineInvalid(t *testing.T) {
	cases := map[string]error{
		`not json`: ErrNotJSON,
		`{"v":1,"title_id":"CUSA03173","trophy_id":1,"unlocked_at":"2026-10-10T06:30:00Z"} {}`: ErrNotJSON,
		`[1,2]`: ErrNotJSON,
		`{"v":2,"title_id":"CUSA03173","trophy_id":1,"unlocked_at":"2026-10-10T06:30:00Z"}`:   ErrVersion,
		`{"title_id":"CUSA03173","trophy_id":1,"unlocked_at":"2026-10-10T06:30:00Z"}`:         ErrVersion,
		`{"v":1,"title_id":"cusa03173","trophy_id":1,"unlocked_at":"2026-10-10T06:30:00Z"}`:   ErrTitleID,
		`{"v":1,"title_id":"CUSA\"03173","trophy_id":1,"unlocked_at":"2026-10-10T06:30:00Z"}`: ErrTitleID,
		`{"v":1,"title_id":"","trophy_id":1,"unlocked_at":"2026-10-10T06:30:00Z"}`:            ErrTitleID,
		`{"v":1,"title_id":"CUSA03173","trophy_id":256,"unlocked_at":"2026-10-10T06:30:00Z"}`: ErrTrophyID,
		`{"v":1,"title_id":"CUSA03173","trophy_id":-1,"unlocked_at":"2026-10-10T06:30:00Z"}`:  ErrTrophyID,
		`{"v":1,"title_id":"CUSA03173","trophy_id":1.5,"unlocked_at":"2026-10-10T06:30:00Z"}`: ErrTrophyID,
		`{"v":1,"title_id":"CUSA03173","trophy_id":"1","unlocked_at":"2026-10-10T06:30:00Z"}`: ErrTrophyID,
		`{"v":"1","title_id":"CUSA03173","trophy_id":1,"unlocked_at":"2026-10-10T06:30:00Z"}`: ErrVersion,
		`{"v":1,"title_id":"CUSA03173","trophy_id":1e1,"unlocked_at":"2026-10-10T06:30:00Z"}`: ErrTrophyID,
		`{"v":1,"title_id":"CUSA03173","unlocked_at":"2026-10-10T06:30:00Z"}`:                 ErrTrophyID,
		`{"v":1,"title_id":"CUSA03173","trophy_id":1,"unlocked_at":"yesterday"}`:              ErrUnlockedAt,
		`{"v":1,"title_id":"CUSA03173","trophy_id":1}`:                                        ErrUnlockedAt,
	}
	for line, want := range cases {
		if _, err := ParseLine([]byte(line)); !errors.Is(err, want) {
			t.Errorf("%s: got %v, want %v", line, err, want)
		}
	}
}

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestReadNewOnlyCompleteLines(t *testing.T) {
	path := filepath.Join(t.TempDir(), FileName)
	res, err := ReadNew(path, Position{}, 1<<20)
	if err != nil || len(res.Entries) != 0 {
		t.Fatalf("missing file should be empty: %v %v", res, err)
	}

	write(t, path, validLine+"\n"+`{"v":1,"title_id":"CUSA03173","trophy_id":13,"unl`)
	res, err = ReadNew(path, Position{}, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Entries) != 1 || res.Entries[0].End != int64(len(validLine)+1) {
		t.Fatalf("want 1 entry ending after the first newline, got %+v", res.Entries)
	}
	if res.Position.Offset != int64(len(validLine)+1) {
		t.Fatalf("offset should stop before the partial line, got %d", res.Position.Offset)
	}

	// the game finishes the line
	f, _ := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	f.WriteString(`ocked_at":"2026-10-10T06:31:00Z"}` + "\n" + "garbage\n")
	f.Close()
	res, err = ReadNew(path, res.Position, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Entries) != 1 || res.Entries[0].Unlock.TrophyID != 13 || len(res.Invalid) != 1 || res.Reset {
		t.Fatalf("want trophy 13 plus one invalid line, got %+v", res)
	}

	// nothing new
	again, _ := ReadNew(path, res.Position, 1<<20)
	if len(again.Entries) != 0 || again.Position != res.Position {
		t.Fatalf("expected no change, got %+v", again)
	}
}

func TestReadNewDetectsTruncation(t *testing.T) {
	path := filepath.Join(t.TempDir(), FileName)
	write(t, path, validLine+"\n"+validLine+"\n")
	res, _ := ReadNew(path, Position{}, 1<<20)
	if len(res.Entries) != 2 {
		t.Fatalf("want 2, got %d", len(res.Entries))
	}

	// truncated to a single line: shorter than the saved offset
	write(t, path, validLine+"\n")
	res, _ = ReadNew(path, res.Position, 1<<20)
	if !res.Reset || len(res.Entries) != 1 {
		t.Fatalf("truncation should restart from 0, got %+v", res)
	}
}

func TestReadNewDetectsRecreatedFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), FileName)
	write(t, path, validLine+"\n")
	res, _ := ReadNew(path, Position{}, 1<<20)

	// deleted and recreated with different content that is just as long or longer
	other := `{"v":1,"title_id":"CUSA03173","trophy_id":99,"unlocked_at":"2026-10-11T06:30:00Z"}`
	write(t, path, other+"\n"+validLine+"\n")
	res, _ = ReadNew(path, res.Position, 1<<20)
	if !res.Reset || len(res.Entries) != 2 || res.Entries[0].Unlock.TrophyID != 99 {
		t.Fatalf("recreated file should be read from 0, got %+v", res)
	}
}

func TestReadNewRespectsMaxBytes(t *testing.T) {
	path := filepath.Join(t.TempDir(), FileName)
	write(t, path, validLine+"\n"+validLine+"\n"+validLine+"\n")
	res, _ := ReadNew(path, Position{}, int64(len(validLine)*2+5))
	if len(res.Entries) != 2 {
		t.Fatalf("want 2 entries within the byte budget, got %d", len(res.Entries))
	}
	res, _ = ReadNew(path, res.Position, 1<<20)
	if len(res.Entries) != 1 || res.Reset {
		t.Fatalf("want the remaining entry, got %+v", res)
	}
}
