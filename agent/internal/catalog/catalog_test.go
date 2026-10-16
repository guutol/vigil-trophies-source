package catalog

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// All trophy files here are synthetic: made-up names, fake PNGs.

var fakePNG = []byte("\x89PNG\r\n\x1a\nsynthetic")

func conf(set string, trophies string) string {
	return `<!--Sce-Np-Trophy-Signature: abc-->
<?xml version="1.0" encoding="UTF-8"?>
<trophyconf version="1.0"><npcommid>` + set + `</npcommid><title-name>Synthetic Game</title-name>
<group id="001"><name>Expansion</name><detail>more</detail></group>` + trophies + `</trophyconf>`
}

const threeTrophies = `<trophy id="000" hidden="no" ttype="P" pid="-1"><name>All</name><detail>Every one</detail></trophy>
<trophy id="001" hidden="yes" ttype="B" pid="000"><name>First</name><detail>One</detail></trophy>
<trophy id="002" hidden="no" ttype="G" pid="-1" gid="001"><name>Extra</name><detail>DLC</detail></trophy>`

func write(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestBuildFromAnRPCS3Folder(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "NPWR00001_00")
	write(t, filepath.Join(dir, "TROPCONF.SFM"), []byte(conf("NPWR00001_00", threeTrophies)))
	write(t, filepath.Join(dir, "TROPUSR.DAT"), []byte("progress, not used"))
	for _, n := range []string{"TROP000.PNG", "TROP001.PNG", "TROP002.PNG", "ICON0.PNG", "GR001.PNG"} {
		write(t, filepath.Join(dir, n), fakePNG)
	}
	write(t, filepath.Join(dir, "TROP009.PNG"), fakePNG)                                   // no such trophy
	write(t, filepath.Join(dir, "GR002.PNG"), fakePNG)                                     // no such group
	write(t, filepath.Join(dir, "TROP003.PNG"), []byte("GIF89a not a png"))                // not a PNG
	write(t, filepath.Join(dir, "TROP004.PNG"), append(fakePNG, make([]byte, 600<<10)...)) // too big

	p, err := Build(Dir{SetID: "NPWR00001_00", Kind: KindRPCS3, Path: dir})
	if err != nil {
		t.Fatal(err)
	}
	if p.Platform != "ps3" || p.Source != "rpcs3" || p.Name != "Synthetic Game" || len(p.Trophies) != 3 {
		t.Fatalf("got %+v", p)
	}
	if p.Trophies[0].Grade != "platinum" || !p.Trophies[1].Hidden || p.Trophies[2].Group == nil || *p.Trophies[2].Group != 1 {
		t.Fatalf("trophies %+v", p.Trophies)
	}
	if len(p.Groups) != 1 || p.Groups[0].ID != 1 || p.Groups[0].Name == nil || *p.Groups[0].Name != "Expansion" {
		t.Fatalf("groups %+v", p.Groups)
	}
	en := p.Texts["en"]
	if len(en.Trophies) != 3 || en.Trophies[1].Name != "First" || en.Trophies[1].Description != "One" {
		t.Fatalf("texts %+v", p.Texts)
	}
	want := []string{"0", "1", "2", "group-1", "icon0"}
	if len(p.Icons) != len(want) {
		t.Fatalf("icons %v", keys(p.Icons))
	}
	for _, k := range want {
		if _, ok := p.Icons[k]; !ok {
			t.Fatalf("missing icon %s in %v", k, keys(p.Icons))
		}
	}
}

func keys(m map[string]string) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	return out
}

func TestBuildFromAShadPS4FolderWithTwoLanguages(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "NPWR00002_00")
	write(t, filepath.Join(dir, "Xml", "TROP.XML"), []byte(conf("NPWR00002_00", threeTrophies)))
	pt := strings.ReplaceAll(strings.ReplaceAll(threeTrophies, "First", "Primeiro"), "All", "Tudo")
	write(t, filepath.Join(dir, "Xml", "TROP_17.XML"), []byte(conf("NPWR00002_00", pt)))
	write(t, filepath.Join(dir, "Icons", "TROP001.PNG"), fakePNG)
	p, err := Build(Dir{SetID: "NPWR00002_00", Kind: KindShadPS4, Path: dir})
	if err != nil {
		t.Fatal(err)
	}
	if p.Platform != "ps4" || p.Texts["pt-BR"].Trophies[1].Name != "Primeiro" || p.Texts["en"].Trophies[1].Name != "First" {
		t.Fatalf("got %+v", p.Texts)
	}
	if len(p.Icons) != 1 {
		t.Fatalf("icons %v", keys(p.Icons))
	}
}

func TestBrokenOrUnsafeFilesAreRefused(t *testing.T) {
	cases := map[string]string{
		"doctype":   `<!DOCTYPE x [<!ENTITY e SYSTEM "file:///etc/passwd">]><trophyconf>&e;</trophyconf>`,
		"entity":    `<trophyconf><!ENTITY a "b"></trophyconf>`,
		"no root":   `<other/>`,
		"truncated": conf("NPWR00003_00", threeTrophies)[:200],
		"ttype":     conf("NPWR00003_00", `<trophy id="001" ttype="X"><name>a</name></trophy>`),
		"id":        conf("NPWR00003_00", `<trophy id="300" ttype="B"><name>a</name></trophy>`),
		"duplicate": conf("NPWR00003_00", `<trophy id="1" ttype="B"><name>a</name></trophy><trophy id="1" ttype="B"><name>b</name></trophy>`),
		"gid":       conf("NPWR00003_00", `<trophy id="1" ttype="B" gid="150"><name>a</name></trophy>`),
		"empty":     conf("NPWR00003_00", ""),
		"deep":      "<trophyconf>" + strings.Repeat("<a>", 20) + strings.Repeat("</a>", 20) + "</trophyconf>",
	}
	for name, data := range cases {
		if _, err := parseXML([]byte(data)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	dir := filepath.Join(t.TempDir(), "NPWR00004_00")
	write(t, filepath.Join(dir, "TROPCONF.SFM"), []byte(conf("NPWR00009_00", threeTrophies)))
	if _, err := Build(Dir{SetID: "NPWR00004_00", Kind: KindRPCS3, Path: dir}); err == nil {
		t.Fatal("npcommid different from the folder must be refused")
	}
	if _, err := Build(Dir{SetID: "NPWR00005_00", Kind: KindRPCS3, Path: t.TempDir()}); !errors.Is(err, ErrNoTexts) {
		t.Fatalf("empty folder: %v", err)
	}
	if _, err := Build(Dir{SetID: "../x", Kind: KindRPCS3, Path: dir}); err == nil {
		t.Fatal("bad set id accepted")
	}
	big := bytes.Repeat([]byte(" "), maxXMLBytes+1)
	write(t, filepath.Join(dir, "TROPCONF.SFM"), big)
	if _, err := Build(Dir{SetID: "NPWR00004_00", Kind: KindRPCS3, Path: dir}); err == nil {
		t.Fatal("oversized XML accepted")
	}
}

func TestBuildNeverWritesTheFolder(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "NPWR00006_00")
	write(t, filepath.Join(dir, "TROPCONF.SFM"), []byte(conf("NPWR00006_00", threeTrophies)))
	before, _ := os.ReadDir(dir)
	info1, _ := os.Stat(filepath.Join(dir, "TROPCONF.SFM"))
	if _, err := Build(Dir{SetID: "NPWR00006_00", Kind: KindRPCS3, Path: dir}); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadDir(dir)
	info2, _ := os.Stat(filepath.Join(dir, "TROPCONF.SFM"))
	if len(before) != len(after) || !info1.ModTime().Equal(info2.ModTime()) {
		t.Fatal("Build changed the folder")
	}
}
