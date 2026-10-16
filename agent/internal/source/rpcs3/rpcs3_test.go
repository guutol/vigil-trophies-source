package rpcs3

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/vigil-tracker/agent/internal/source"
)

// All files here are synthetic, built byte by byte from the documented layout.

type entry struct {
	id       uint32
	unlocked bool
	tick     uint64
}

func unixTick(t time.Time) uint64 { return uint64(t.UnixMicro()) + rtcEpochOffset }

// buildTROPUSR writes a header, a type-4 table (ignored by the parser) and a type-6 table.
func buildTROPUSR(entries []entry) []byte {
	be := binary.BigEndian
	const tables = 2
	type4Offset := uint64(headerSize + tables*tableHeaderSize)
	type6Offset := type4Offset + uint64(len(entries))*0x60
	data := make([]byte, type6Offset+uint64(len(entries))*entry6Size)
	be.PutUint32(data[0:], tropusrMagic)
	be.PutUint32(data[4:], 0x00010000)
	be.PutUint32(data[8:], tables)

	h4 := data[headerSize:]
	be.PutUint32(h4[0:], 4)
	be.PutUint32(h4[4:], 0x50)
	be.PutUint32(h4[8:], 1)
	be.PutUint32(h4[12:], uint32(len(entries)))
	be.PutUint64(h4[16:], type4Offset)

	h6 := data[headerSize+tableHeaderSize:]
	be.PutUint32(h6[0:], 6)
	be.PutUint32(h6[4:], 0x60)
	be.PutUint32(h6[8:], 1)
	be.PutUint32(h6[12:], uint32(len(entries)))
	be.PutUint64(h6[16:], type6Offset)

	for i, e := range entries {
		e4 := data[type4Offset+uint64(i)*0x60:]
		be.PutUint32(e4[0:], 4)
		be.PutUint32(e4[16:], e.id)
		e6 := data[type6Offset+uint64(i)*entry6Size:]
		be.PutUint32(e6[0:], 6)
		be.PutUint32(e6[4:], 0x60)
		be.PutUint32(e6[8:], uint32(i))
		be.PutUint32(e6[16:], e.id)
		if e.unlocked {
			be.PutUint32(e6[20:], 1)
			be.PutUint64(e6[32:], e.tick)
			be.PutUint64(e6[40:], e.tick)
		}
	}
	return data
}

var when = time.Date(2026, 10, 10, 6, 30, 0, 0, time.UTC)

func TestParseTROPUSR(t *testing.T) {
	data := buildTROPUSR([]entry{{0, false, 0}, {1, true, unixTick(when)}, {2, true, 0}, {3, false, 0}})
	got, err := ParseTROPUSR(data)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 4 || got[1].ID != 1 || !got[1].Unlocked || !got[1].Time.Equal(when) {
		t.Fatalf("got %+v", got)
	}
	if !got[2].Unlocked || !got[2].Time.IsZero() {
		t.Fatalf("unlocked without time: %+v", got[2])
	}
	if got[0].Unlocked || got[3].Unlocked {
		t.Fatal("locked trophies reported as unlocked")
	}
}

func TestParseTROPUSRRejectsBrokenFiles(t *testing.T) {
	good := buildTROPUSR([]entry{{1, true, unixTick(when)}, {2, false, 0}})
	be := binary.BigEndian
	mutate := func(f func(b []byte)) []byte {
		b := append([]byte(nil), good...)
		f(b)
		return b
	}
	type6 := headerSize + tableHeaderSize
	cases := map[string][]byte{
		"empty":             {},
		"short header":      good[:headerSize-1],
		"truncated tables":  good[:headerSize+tableHeaderSize],
		"truncated entries": good[:len(good)-1],
		"bad magic":         mutate(func(b []byte) { b[0] = 0 }),
		"no tables":         mutate(func(b []byte) { be.PutUint32(b[8:], 0) }),
		"too many tables":   mutate(func(b []byte) { be.PutUint32(b[8:], 1000) }),
		"huge count":        mutate(func(b []byte) { be.PutUint32(b[type6+12:], 0xFFFFFFFF) }),
		"offset past end":   mutate(func(b []byte) { be.PutUint64(b[type6+16:], uint64(len(good))) }),
		"offset overflow":   mutate(func(b []byte) { be.PutUint64(b[type6+16:], 0xFFFFFFFFFFFFFFF0) }),
		"offset in header":  mutate(func(b []byte) { be.PutUint64(b[type6+16:], 4) }),
		"no unlock table":   mutate(func(b []byte) { be.PutUint32(b[type6:], 5) }),
		"two unlock tables": mutate(func(b []byte) { be.PutUint32(b[headerSize:], 6) }),
		"trophy id > 255": mutate(func(b []byte) {
			off := be.Uint64(b[type6+16:])
			be.PutUint32(b[off+16:], 256)
		}),
		"duplicate trophy": mutate(func(b []byte) {
			off := be.Uint64(b[type6+16:])
			be.PutUint32(b[off+entry6Size+16:], 1)
		}),
		"wrong entry type": mutate(func(b []byte) {
			off := be.Uint64(b[type6+16:])
			be.PutUint32(b[off:], 4)
		}),
		"too large": make([]byte, MaxFileSize+1),
	}
	for name, data := range cases {
		if _, err := ParseTROPUSR(data); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestTickConversionBounds(t *testing.T) {
	if !tickToTime(0).IsZero() || !tickToTime(rtcEpochOffset).IsZero() {
		t.Fatal("ticks at or before 1970 must be zero")
	}
	if !tickToTime(^uint64(0)).IsZero() {
		t.Fatal("ticks after 2100 must be zero")
	}
	if got := tickToTime(unixTick(when)); !got.Equal(when) {
		t.Fatalf("got %v", got)
	}
}

func writeSet(t *testing.T, root, set string, data []byte) string {
	t.Helper()
	dir := filepath.Join(root, "dev_hdd0", "home", "00000001", "trophy", set)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "TROPUSR.DAT")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func fingerprint(t *testing.T, path string) [2]any {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	info, _ := os.Stat(path)
	return [2]any{sha256.Sum256(data), info.ModTime()}
}

func TestSourceScansEverySetAndOnlyReads(t *testing.T) {
	root := t.TempDir()
	a := writeSet(t, root, "NPWR00001_00", buildTROPUSR([]entry{{0, false, 0}, {1, true, unixTick(when)}, {5, true, 0}}))
	b := writeSet(t, root, "NPWR00002_00", []byte("garbage, not a TROPUSR file"))
	writeSet(t, root, "not-a-set", buildTROPUSR([]entry{{1, true, 0}}))
	os.MkdirAll(filepath.Join(root, "dev_hdd0", "home", "00000001", "trophy", "NPWR00003_00"), 0o700) // never played
	before := [][2]any{fingerprint(t, a), fingerprint(t, b)}

	s, _ := New(root)
	got, err := s.Scan(context.Background())
	if err == nil {
		t.Fatal("the corrupt set must be reported")
	}
	if len(got) != 2 || got[0].TrophySetID != "NPWR00001_00" || got[0].TrophyID != 1 ||
		got[0].UnlockedAt != "2026-10-10T06:30:00Z" || got[1].TrophyID != 5 || got[1].UnlockedAt != "" ||
		got[0].Source != source.RPCS3 || got[0].TitleID != "" {
		t.Fatalf("got %+v", got)
	}
	if after := [][2]any{fingerprint(t, a), fingerprint(t, b)}; after[0] != before[0] || after[1] != before[1] {
		t.Fatal("scan modified a file")
	}

	// unchanged files are not parsed again: the broken one is no longer reported
	if _, err := s.Scan(context.Background()); err != nil {
		t.Fatalf("second scan re-read unchanged files: %v", err)
	}

	// a changed file is parsed again
	os.WriteFile(a, buildTROPUSR([]entry{{0, false, 0}, {1, true, unixTick(when)}, {5, true, 0}, {6, true, 0}}), 0o600)
	got, _ = s.Scan(context.Background())
	if len(got) != 3 {
		t.Fatalf("changed file not re-read: %+v", got)
	}
}

func TestTrophyDirsAcceptsSeveralStartingPoints(t *testing.T) {
	root := t.TempDir()
	writeSet(t, root, "NPWR00001_00", buildTROPUSR([]entry{{1, true, 0}}))
	os.MkdirAll(filepath.Join(root, "dev_hdd0", "home", "12345", "trophy"), 0o700) // not an 8-digit user
	for _, start := range []string{
		root,
		filepath.Join(root, "dev_hdd0"),
		filepath.Join(root, "dev_hdd0", "home", "00000001"),
		filepath.Join(root, "dev_hdd0", "home", "00000001", "trophy"),
	} {
		if dirs := TrophyDirs(start); len(dirs) != 1 {
			t.Errorf("%s: got %v", start, dirs)
		}
	}
	if dirs := TrophyDirs(t.TempDir()); len(dirs) != 0 {
		t.Fatalf("empty folder: %v", dirs)
	}
}

func TestOversizedFileIsRefusedWithoutReadingIt(t *testing.T) {
	root := t.TempDir()
	writeSet(t, root, "NPWR00001_00", make([]byte, MaxFileSize+10))
	s, _ := New(root)
	_, err := s.Scan(context.Background())
	if !errors.Is(err, source.ErrTooLarge) {
		t.Fatalf("want ErrTooLarge, got %v", err)
	}
}
