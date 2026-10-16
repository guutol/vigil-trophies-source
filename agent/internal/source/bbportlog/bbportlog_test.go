package bbportlog

import (
	"context"
	"encoding/binary"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/vigil-tracker/agent/internal/source"
)

// All logs and game files here are synthetic.

func TestParseLine(t *testing.T) {
	valid := map[string]int{
		"Runtime: trophy 0 unlocked":   0,
		"Runtime: trophy 12 unlocked":  12,
		"Runtime: trophy 255 unlocked": 255,
	}
	for line, want := range valid {
		if got, ok := ParseLine(line); !ok || got != want {
			t.Errorf("%q: got %d %v", line, got, ok)
		}
	}
	invalid := []string{
		"Runtime: trophy 256 unlocked",
		"Runtime: trophy 1000 unlocked",
		"Runtime: trophy -1 unlocked",
		"Runtime: trophy 012 unlocked",
		"Runtime: trophy  1 unlocked",
		"Runtime: trophy 1 unlocked!",
		"Runtime: trophy 1 unlocked extra",
		" Runtime: trophy 1 unlocked",
		"xRuntime: trophy 1 unlocked",
		"runtime: trophy 1 unlocked",
		"Runtime: trophy 1 not recorded in user/trophies.jsonl",
		"Runtime: trophy one unlocked",
		"Runtime: trophy 1 unlocked\r",
		"Runtime: trophy 1 unlocked\nRuntime: trophy 2 unlocked",
		"",
	}
	for _, line := range invalid {
		if id, ok := ParseLine(line); ok {
			t.Errorf("%q accepted as %d", line, id)
		}
	}
}

type fixture struct {
	t    *testing.T
	path string
	src  *Source
	now  time.Time
}

func newFixture(t *testing.T, game Game) *fixture {
	t.Helper()
	dir := t.TempDir()
	src, err := ForUserDir(dir, game)
	if err != nil {
		t.Fatal(err)
	}
	f := &fixture{t: t, path: filepath.Join(dir, LogName), src: src, now: time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)}
	src.Now = func() time.Time { return f.now }
	return f
}

func (f *fixture) write(content string, appendIt bool) {
	f.t.Helper()
	flags := os.O_CREATE | os.O_WRONLY | os.O_TRUNC
	if appendIt {
		flags = os.O_CREATE | os.O_WRONLY | os.O_APPEND
	}
	file, err := os.OpenFile(f.path, flags, 0o600)
	if err != nil {
		f.t.Fatal(err)
	}
	file.WriteString(content)
	file.Close()
	// make sure the change is visible even on filesystems with coarse mtimes
	f.now = f.now.Add(time.Minute)
	os.Chtimes(f.path, f.now, f.now)
}

func (f *fixture) ids() []int {
	f.t.Helper()
	got, err := f.src.Scan(context.Background())
	if err != nil {
		f.t.Fatal(err)
	}
	var ids []int
	for _, u := range got {
		ids = append(ids, u.TrophyID)
	}
	return ids
}

func TestScanFollowsTheLog(t *testing.T) {
	f := newFixture(t, Game{TrophySetID: "NPWR05818_00", TitleID: "CUSA03173"})
	if ids := f.ids(); len(ids) != 0 {
		t.Fatalf("missing file: %v", ids)
	}
	f.write("Bloodborne | entry=0xa0\r\nRuntime: trophy 12 unlocked\r\nRuntime: trophy 300 unlocked\n"+
		"Runtime: trophy 1 unlocked!\nRuntime: trophy 13 not recorded in x\n", false)
	got, _ := f.src.Scan(context.Background())
	if len(got) != 1 {
		t.Fatalf("got %+v", got)
	}
	u := got[0]
	if u.Source != source.BBPortLog || u.TrophySetID != "NPWR05818_00" || u.TitleID != "CUSA03173" ||
		u.TrophyID != 12 || u.UnlockedAt != "2026-10-11T12:01:00Z" {
		t.Fatalf("unlock %+v", u)
	}

	f.write("Runtime: trophy 4", true) // still being written: not read yet
	if ids := f.ids(); !slices.Equal(ids, []int{12}) {
		t.Fatalf("partial line read: %v", ids)
	}
	f.write("0 unlocked\nRuntime: trophy 12 unlocked\n", true) // completes "40"; 12 again collapses
	if ids := f.ids(); !slices.Equal(ids, []int{12, 40}) {
		t.Fatalf("after append: %v", ids)
	}
	if got, _ := f.src.Scan(context.Background()); got[0].UnlockedAt != "2026-10-11T12:01:00Z" {
		t.Fatal("the first time a trophy was seen must be kept")
	}
}

func TestRewrittenLogIsReadFromTheStart(t *testing.T) {
	f := newFixture(t, Game{})
	banner := "Bloodborne | entry=0xa0\nGPU: something\n"
	f.write(banner+"Runtime: trophy 1 unlocked\n", false)
	if ids := f.ids(); !slices.Equal(ids, []int{1}) {
		t.Fatalf("first run: %v", ids)
	}
	// new run, shorter: truncated
	f.write("Runtime: trophy 2 unlocked\n", false)
	if ids := f.ids(); !slices.Equal(ids, []int{1, 2}) {
		t.Fatalf("after truncation: %v", ids)
	}
	// new run with the same banner, already longer than the old offset: the bytes before the
	// old offset changed, so it is read again from the start
	f.write(banner+"Runtime: trophy 3 unlocked\n"+strings.Repeat("noise line\n", 20)+"Runtime: trophy 5 unlocked\n", false)
	if ids := f.ids(); !slices.Equal(ids, []int{1, 2, 3, 5}) {
		t.Fatalf("after rewrite: %v", ids)
	}
}

func TestLongLinesAreSkipped(t *testing.T) {
	f := newFixture(t, Game{})
	f.write(strings.Repeat("x", 3*maxRead/2)+"Runtime: trophy 1 unlocked\nRuntime: trophy 2 unlocked\n", false)
	if ids := f.ids(); !slices.Equal(ids, []int{2}) {
		t.Fatalf("got %v", ids)
	}
	f.write(strings.Repeat("y", MaxLineLength+1)+"\nRuntime: trophy 3 unlocked\n", true)
	if ids := f.ids(); !slices.Equal(ids, []int{2, 3}) {
		t.Fatalf("got %v", ids)
	}
}

func TestWithoutAGameFolderBloodborneIsAssumed(t *testing.T) {
	f := newFixture(t, Game{})
	f.write("Runtime: trophy 7 unlocked\n", false)
	got, _ := f.src.Scan(context.Background())
	if len(got) != 1 || got[0].TitleID != DefaultTitleID || got[0].TrophySetID != "" {
		t.Fatalf("got %+v", got)
	}
}

func TestScanNeverWritesTheLog(t *testing.T) {
	f := newFixture(t, Game{})
	f.write("Runtime: trophy 7 unlocked\n", false)
	before, _ := os.ReadFile(f.path)
	info1, _ := os.Stat(f.path)
	f.ids()
	after, _ := os.ReadFile(f.path)
	info2, _ := os.Stat(f.path)
	if string(before) != string(after) || !info1.ModTime().Equal(info2.ModTime()) {
		t.Fatal("scan changed the log")
	}
}

// sfo builds a minimal param.sfo with string entries.
func sfo(entries map[string]string) []byte {
	keys := []string{}
	for k := range entries {
		keys = append(keys, k)
	}
	var keyTable, dataTable, index []byte
	for _, k := range keys {
		v := append([]byte(entries[k]), 0)
		e := make([]byte, 16)
		binary.LittleEndian.PutUint16(e[0:], uint16(len(keyTable)))
		binary.LittleEndian.PutUint16(e[2:], 0x0204)
		binary.LittleEndian.PutUint32(e[4:], uint32(len(v)))
		binary.LittleEndian.PutUint32(e[8:], uint32(len(v)))
		binary.LittleEndian.PutUint32(e[12:], uint32(len(dataTable)))
		index = append(index, e...)
		keyTable = append(keyTable, append([]byte(k), 0)...)
		dataTable = append(dataTable, v...)
	}
	head := make([]byte, 20)
	copy(head, "\x00PSF")
	binary.LittleEndian.PutUint32(head[4:], 0x101)
	binary.LittleEndian.PutUint32(head[8:], uint32(20+len(index)))
	binary.LittleEndian.PutUint32(head[12:], uint32(20+len(index)+len(keyTable)))
	binary.LittleEndian.PutUint32(head[16:], uint32(len(keys)))
	return append(append(append(head, index...), keyTable...), dataTable...)
}

func TestIdentifyReadsTheGameFolder(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "sce_sys"), 0o700)
	os.WriteFile(filepath.Join(dir, "sce_sys", "param.sfo"), sfo(map[string]string{"TITLE": "Game", "TITLE_ID": "CUSA00001"}), 0o600)
	os.WriteFile(filepath.Join(dir, "sce_sys", "npbind.dat"), []byte("\x00\x01NPWR00001_00\x00junk"), 0o600)
	g, err := Identify(dir)
	if err != nil || g.TitleID != "CUSA00001" || g.TrophySetID != "NPWR00001_00" {
		t.Fatalf("got %+v %v", g, err)
	}
	if g, err := Identify(t.TempDir()); err != nil || g != (Game{}) {
		t.Fatalf("empty folder: %+v %v", g, err)
	}
}

func TestBrokenGameFilesAreErrorsNotPanics(t *testing.T) {
	good := sfo(map[string]string{"TITLE_ID": "CUSA00001"})
	bad := [][]byte{
		nil,
		[]byte("\x00PSF"),
		append([]byte("XPSF"), good[4:]...),
		good[:30],
		sfo(map[string]string{"TITLE_ID": "cusa\"0001"}),
	}
	huge := append([]byte(nil), good...)
	binary.LittleEndian.PutUint32(huge[16:], 0xFFFFFFFF) // entry count
	bad = append(bad, huge)
	offset := append([]byte(nil), good...)
	binary.LittleEndian.PutUint32(offset[8:], 0x7FFFFFFF) // key table far outside
	bad = append(bad, offset)
	for i, data := range bad {
		if title, err := TitleIDFromSFO(data); err == nil && title != "" {
			t.Errorf("case %d accepted: %q", i, title)
		}
	}
	if NPCommIDFromNPBind([]byte("NPWR00001_00 NPWR00002_00")) != "" {
		t.Fatal("two different ids must give none")
	}
	if NPCommIDFromNPBind([]byte("nothing")) != "" {
		t.Fatal("no id")
	}
}

// The launcher's two ways of starting the game write different line ends: "\r\n", or "\r\r\n" when
// its text-mode file translates the game's own "\r\n" again (seen in a real last_run.log). Both are
// read; anything else after the line keeps it from matching.
func TestDoubledCarriageReturnsAreRead(t *testing.T) {
	f := newFixture(t, Game{TrophySetID: "NPWR05818_00"})
	f.write("Bloodborne | entry=0xa0\r\r\n"+
		"Runtime: guest thread 'CSTrophy::AsyncUpdateThread' created (stack=65536, prio=700)\r\r\n"+
		"Runtime: trophy 21 unlocked\r\r\n", false)
	if ids := f.ids(); !slices.Equal(ids, []int{21}) {
		t.Fatalf("\r\r\n line: %v", ids)
	}
	f.write("Runtime: trophy 22 unlocked\r\r\r\nRuntime: trophy 23 unlocked \r\n", true)
	if ids := f.ids(); !slices.Equal(ids, []int{21, 22}) {
		t.Fatalf("only carriage returns are dropped: %v", ids)
	}
}
