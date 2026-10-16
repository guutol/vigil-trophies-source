package shadps4

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vigil-tracker/agent/internal/source"
)

// All files here are synthetic: made-up ids, no names or icons from any game.

const when = 1760077800 // 2025-10-10T06:30:00Z

func trophyXML(npcommid string, trophies string) string {
	return `<?xml version="1.0" encoding="UTF-8"?>
<!--Trophy data written by the emulator-->
<trophyconf version="1.1" policy="large">
  <npcommid>` + npcommid + `</npcommid>
  <trophyset-version>01.00</trophyset-version>
  ` + trophies + `
</trophyconf>`
}

func trophy(id int, unlocked bool, stamp string) string {
	return fmt.Sprintf(`<trophy id="%03d" hidden="no" ttype="B" pid="000" unlockstate="%t" timestamp="%s"><name>T</name></trophy>`,
		id, unlocked, stamp)
}

func TestParseTrophyXML(t *testing.T) {
	data := trophyXML("NPWR00001_00",
		trophy(0, false, "0")+trophy(1, true, fmt.Sprint(when))+trophy(2, true, "")+trophy(3, true, fmt.Sprint(int64(when)*1000)))
	f, err := ParseTrophyXML([]byte(data))
	if err != nil {
		t.Fatal(err)
	}
	if f.NPCommID != "NPWR00001_00" || len(f.Trophies) != 4 {
		t.Fatalf("got %+v", f)
	}
	if f.Trophies[0].Unlocked || !f.Trophies[1].Unlocked || f.Trophies[1].Time.Unix() != when {
		t.Fatalf("trophy 1: %+v", f.Trophies[1])
	}
	if !f.Trophies[2].Time.IsZero() {
		t.Fatal("missing timestamp must give zero time")
	}
	if f.Trophies[3].Time.Unix() != when {
		t.Fatalf("millisecond timestamp: %v", f.Trophies[3].Time)
	}
}

func TestParseTrophyXMLRejectsUnsafeOrBrokenFiles(t *testing.T) {
	cases := map[string]string{
		"doctype":           `<?xml version="1.0"?><!DOCTYPE t [<!ENTITY x SYSTEM "file:///etc/passwd">]><trophyconf>&x;</trophyconf>`,
		"entity":            `<trophyconf><!entity x "y"></trophyconf>`,
		"external":          `<?xml version="1.0"?><!DOCTYPE trophyconf SYSTEM "http://example.invalid/x.dtd"><trophyconf/>`,
		"undeclared entity": `<trophyconf>&nope;</trophyconf>`,
		"truncated":         trophyXML("NPWR00001_00", trophy(1, true, "1"))[:120],
		"not xml":           "garbage",
		"empty":             "",
		"bad id":            trophyXML("NPWR00001_00", `<trophy id="abc" unlockstate="true"/>`),
		"id > 255":          trophyXML("NPWR00001_00", `<trophy id="256" unlockstate="true"/>`),
		"negative":          trophyXML("NPWR00001_00", `<trophy id="-1" unlockstate="true"/>`),
		"no id":             trophyXML("NPWR00001_00", `<trophy unlockstate="true"/>`),
		"duplicate":         trophyXML("NPWR00001_00", trophy(1, true, "1")+trophy(1, false, "")),
		"encoding":          `<?xml version="1.0" encoding="UTF-16"?><trophyconf/>`,
		"too deep":          strings.Repeat("<a>", 20) + strings.Repeat("</a>", 20),
		"too large":         "<trophyconf>" + strings.Repeat(" ", MaxFileSize) + "</trophyconf>",
	}
	for name, data := range cases {
		if _, err := ParseTrophyXML([]byte(data)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if _, err := ParseTrophyXML([]byte(cases["doctype"])); !errors.Is(err, ErrDTD) {
		t.Fatalf("doctype: want ErrDTD, got %v", err)
	}
}

func TestImplausibleTimestampsAreDropped(t *testing.T) {
	for _, stamp := range []string{"-5", "abc", "1", "99999999999999999999999"} {
		f, err := ParseTrophyXML([]byte(trophyXML("NPWR00001_00", trophy(1, true, stamp))))
		if err != nil {
			t.Fatalf("%s: %v", stamp, err)
		}
		if got := source.FormatTime(f.Trophies[0].Time); got != "" {
			t.Errorf("%s: got %q", stamp, got)
		}
	}
}

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestSourceReadsCurrentAndOldLayouts(t *testing.T) {
	emu := t.TempDir()
	user := filepath.Join(emu, "user")
	write(t, filepath.Join(user, "home", "1000", "trophy", "NPWR00001_00.xml"),
		trophyXML("NPWR00001_00", trophy(0, false, "")+trophy(1, true, fmt.Sprint(when))))
	write(t, filepath.Join(user, "home", "1000", "trophy", "NPWR00002_00.xml"), "<broken")
	write(t, filepath.Join(user, "home", "1000", "trophy", "notes.xml"), trophyXML("NPWR00009_00", trophy(1, true, "")))
	write(t, filepath.Join(user, "home", "1000", "trophy", "NPWR00004_00.xml"),
		trophyXML("NPWR00005_00", trophy(1, true, ""))) // npcommid does not match the file name
	write(t, filepath.Join(user, "game_data", "CUSA00003", "TrophyFiles", "trophy00", "Xml", "TROP.XML"),
		trophyXML("NPWR00003_00", trophy(7, true, fmt.Sprint(when))))
	// static data shadPS4 extracts: never read as unlocks
	write(t, filepath.Join(user, "trophy", "NPWR00001_00", "Xml", "TROP.XML"), trophyXML("NPWR00001_00", trophy(9, true, "")))

	s, _ := New(emu) // the emulator folder: resolves to user/
	if s.Root() != user {
		t.Fatalf("root %s", s.Root())
	}
	got, err := s.Scan(context.Background())
	if err == nil {
		t.Fatal("broken and mismatched files must be reported")
	}
	if len(got) != 2 {
		t.Fatalf("got %+v", got)
	}
	cur, old := got[0], got[1]
	if old.TrophySetID == "NPWR00001_00" {
		cur, old = old, cur
	}
	if cur.TrophySetID != "NPWR00001_00" || cur.TrophyID != 1 || cur.UnlockedAt != "2025-10-10T06:30:00Z" ||
		cur.TitleID != "" || cur.Source != source.ShadPS4 {
		t.Fatalf("current layout: %+v", cur)
	}
	if old.TrophySetID != "NPWR00003_00" || old.TitleID != "CUSA00003" || old.TrophyID != 7 {
		t.Fatalf("old layout: %+v", old)
	}
	if _, err := s.Scan(context.Background()); err != nil {
		t.Fatalf("unchanged files parsed again: %v", err)
	}
}

func TestEmptyUserFolderHasNoUnlocks(t *testing.T) {
	user := t.TempDir()
	for _, d := range []string{"home", "trophy", "game_data", "sys_modules"} {
		os.MkdirAll(filepath.Join(user, d), 0o700)
	}
	s, _ := New(user)
	got, err := s.Scan(context.Background())
	if err != nil || len(got) != 0 {
		t.Fatalf("got %v %v", got, err)
	}
}
