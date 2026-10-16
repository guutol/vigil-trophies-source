package source

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestFormatTimeWindow(t *testing.T) {
	if FormatTime(time.Time{}) != "" || FormatTime(time.Date(1999, 12, 31, 0, 0, 0, 0, time.UTC)) != "" ||
		FormatTime(time.Date(2100, 1, 1, 0, 0, 0, 0, time.UTC)) != "" {
		t.Fatal("out-of-window times must be empty")
	}
	local := time.Date(2026, 10, 10, 3, 30, 0, 0, time.FixedZone("BRT", -3*3600))
	if got := FormatTime(local); got != "2026-10-10T06:30:00Z" {
		t.Fatalf("got %s", got)
	}
}

func TestEventIDFormat(t *testing.T) {
	id := EventID(Unlock{Source: RPCS3, TrophySetID: "NPWR00001_00", TrophyID: 1})
	if len(id) != 36 || id[14] != '5' {
		t.Fatalf("not a v5 uuid: %s", id)
	}
}

func TestCacheParsesOnlyChangedFiles(t *testing.T) {
	path := filepath.Join(t.TempDir(), "f")
	var c Cache
	calls := 0
	parse := func(string) ([]Unlock, error) {
		calls++
		if calls == 2 {
			return nil, errors.New("broken")
		}
		return []Unlock{{TrophyID: calls}}, nil
	}
	if got, err := c.Get(path, parse); got != nil || err != nil || calls != 0 {
		t.Fatal("missing file must not be parsed")
	}
	os.WriteFile(path, []byte("a"), 0o600)
	c.Get(path, parse)
	if got, _ := c.Get(path, parse); calls != 1 || len(got) != 1 {
		t.Fatalf("unchanged file parsed again: calls=%d", calls)
	}
	os.WriteFile(path, []byte("ab"), 0o600)
	if _, err := c.Get(path, parse); err == nil || calls != 2 {
		t.Fatal("changed file must be parsed and its error returned")
	}
	if got, err := c.Get(path, parse); err != nil || got != nil || calls != 2 {
		t.Fatal("a broken file is reported once, then remembered as empty")
	}
}

func TestReadLimited(t *testing.T) {
	path := filepath.Join(t.TempDir(), "f")
	os.WriteFile(path, []byte("12345"), 0o600)
	if _, err := ReadLimited(path, 4); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("want ErrTooLarge, got %v", err)
	}
	if data, err := ReadLimited(path, 5); err != nil || string(data) != "12345" {
		t.Fatalf("got %q %v", data, err)
	}
}
