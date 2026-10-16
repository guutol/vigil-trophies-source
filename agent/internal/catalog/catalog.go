// Package catalog builds catalog proposals from trophy data an emulator already keeps in plain
// files: RPCS3's TROPCONF.SFM + PNGs and shadPS4's Xml/TROP*.XML + Icons/. The server publishes a
// trophy set once three different users send the same content. Files are only read; nothing is
// decrypted.
package catalog

import (
	"bytes"
	"encoding/base64"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/vigil-tracker/agent/internal/source"
)

// Kinds of folders a proposal can be built from.
const (
	KindRPCS3   = "rpcs3"   // <...>/trophy/<NPWR>/ with TROPCONF.SFM and PNGs
	KindShadPS4 = "shadps4" // <user>/trophy/<NPWR>/ with Xml/ and Icons/
)

const (
	maxXMLBytes   = 4 << 20
	maxIconBytes  = 512 << 10
	maxIconsTotal = 12 << 20
)

// Dir is a folder with the static trophy data of one set.
type Dir struct {
	SetID string
	Kind  string
	Path  string
}

// Provider is implemented by sources that know where such folders are.
type Provider interface {
	CatalogDirs() []Dir
}

// Proposal is what the agent sends to POST /api/agent/catalog-proposals.
type Proposal struct {
	TrophySetID string               `json:"trophy_set_id"`
	Platform    string               `json:"platform"`
	Source      string               `json:"source"`
	TitleIDs    []string             `json:"title_ids"`
	Name        string               `json:"name"`
	Groups      []Group              `json:"groups"`
	Trophies    []Trophy             `json:"trophies"`
	Texts       map[string]LangTexts `json:"texts"`
	Icons       map[string]string    `json:"icons"`
}

// Group is a DLC group (1..99).
type Group struct {
	ID   int     `json:"id"`
	Name *string `json:"name,omitempty"`
}

// Trophy is the structure of one trophy; Group is nil for the base game.
type Trophy struct {
	ID     int    `json:"id"`
	Grade  string `json:"grade"`
	Hidden bool   `json:"hidden"`
	Group  *int   `json:"group,omitempty"`
}

// LangTexts are the texts of one language.
type LangTexts struct {
	Name     string `json:"name"`
	Trophies []Text `json:"trophies"`
}

// Text is one trophy's name and description.
type Text struct {
	ID          int    `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
}

// Errors for unusable folders. Messages never include file content.
var (
	ErrNoTexts = errors.New("no trophy texts in the folder")
	ErrXML     = errors.New("not a usable trophy XML")
)

var (
	grades  = map[string]string{"P": "platinum", "G": "gold", "S": "silver", "B": "bronze"}
	idRe    = regexp.MustCompile(`^[0-9]{1,3}$`)
	iconRe  = regexp.MustCompile(`^(?i)(TROP([0-9]{3})|ICON0|GR([0-9]{3}))\.PNG$`)
	setIDRe = source.TrophySetIDPattern
)

// parsed is one trophy XML file.
type parsed struct {
	npcommid string
	title    string
	groups   map[int]string
	trophies []parsedTrophy
}

type parsedTrophy struct {
	id     int
	grade  string
	hidden bool
	group  *int
	name   string
	detail string
}

// Build reads a folder and returns its proposal.
func Build(d Dir) (*Proposal, error) {
	if !setIDRe.MatchString(d.SetID) {
		return nil, fmt.Errorf("invalid trophy set id")
	}
	var texts map[string]*parsed
	var iconDir string
	var err error
	p := &Proposal{TrophySetID: d.SetID, Source: d.Kind, TitleIDs: []string{}, Texts: map[string]LangTexts{},
		Icons: map[string]string{}, Groups: []Group{}}
	switch d.Kind {
	case KindRPCS3:
		p.Platform = "ps3"
		// RPCS3 keeps one text file, in the language RPCS3 is set to (English by default).
		texts, err = readTexts(d.Path, map[string][]string{"en": {"TROPCONF.SFM", "TROP.SFM"}})
		iconDir = d.Path
	case KindShadPS4:
		p.Platform = "ps4"
		texts, err = readTexts(filepath.Join(d.Path, "Xml"), map[string][]string{
			"en": {"TROP_01.XML", "TROP.XML"}, "pt-BR": {"TROP_17.XML"}})
		iconDir = filepath.Join(d.Path, "Icons")
	default:
		return nil, fmt.Errorf("unknown kind %q", d.Kind)
	}
	if err != nil {
		return nil, err
	}
	base := texts["en"]
	if base == nil {
		for _, t := range texts {
			base = t
		}
	}
	if base.npcommid != "" && base.npcommid != d.SetID {
		return nil, fmt.Errorf("npcommid does not match the folder")
	}
	p.Name = strings.TrimSpace(base.title)
	if p.Name == "" {
		return nil, ErrXML
	}
	gids := make([]int, 0, len(base.groups))
	for id := range base.groups {
		gids = append(gids, id)
	}
	sort.Ints(gids)
	for _, id := range gids {
		g := Group{ID: id}
		if n := strings.TrimSpace(base.groups[id]); n != "" {
			g.Name = &n
		}
		p.Groups = append(p.Groups, g)
	}
	for _, t := range base.trophies {
		p.Trophies = append(p.Trophies, Trophy{ID: t.id, Grade: t.grade, Hidden: t.hidden, Group: t.group})
	}
	for lang, f := range texts {
		lt := LangTexts{Name: strings.TrimSpace(f.title)}
		if len(f.trophies) != len(base.trophies) {
			continue // a translation with other trophies: leave it out
		}
		for _, t := range f.trophies {
			lt.Trophies = append(lt.Trophies, Text{ID: t.id, Name: strings.TrimSpace(t.name),
				Description: strings.TrimSpace(t.detail)})
		}
		p.Texts[lang] = lt
	}
	if err := readIcons(iconDir, p); err != nil {
		return nil, err
	}
	return p, nil
}

func readTexts(dir string, wanted map[string][]string) (map[string]*parsed, error) {
	out := map[string]*parsed{}
	for lang, names := range wanted {
		for _, name := range names {
			data, err := source.ReadLimited(filepath.Join(dir, name), maxXMLBytes)
			if err != nil {
				continue
			}
			f, err := parseXML(data)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", name, err)
			}
			out[lang] = f
			break
		}
	}
	if len(out) == 0 {
		return nil, ErrNoTexts
	}
	return out, nil
}

// parseXML reads a trophyconf document. DTDs and entity declarations are refused before parsing;
// anything before <trophyconf> (a signature comment, a late XML declaration) is dropped.
func parseXML(data []byte) (*parsed, error) {
	upper := bytes.ToUpper(data)
	if bytes.Contains(upper, []byte("<!DOCTYPE")) || bytes.Contains(upper, []byte("<!ENTITY")) {
		return nil, ErrXML
	}
	start := bytes.Index(data, []byte("<trophyconf"))
	if start < 0 {
		return nil, ErrXML
	}
	dec := xml.NewDecoder(bytes.NewReader(data[start:]))
	dec.Strict = true
	out := &parsed{groups: map[int]string{}}
	seen := map[int]bool{}
	var stack []string
	var cur *parsedTrophy
	var curGroup = -1
	var text strings.Builder
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, ErrXML
		}
		switch t := tok.(type) {
		case xml.StartElement:
			stack = append(stack, t.Name.Local)
			if len(stack) > 8 {
				return nil, ErrXML
			}
			text.Reset()
			switch t.Name.Local {
			case "trophy":
				tr, err := trophyAttrs(t)
				if err != nil || seen[tr.id] {
					return nil, ErrXML
				}
				seen[tr.id] = true
				cur = tr
			case "group":
				id, ok := attrInt(t, "id")
				if !ok || id < 1 || id > 99 {
					return nil, ErrXML
				}
				curGroup = id
				out.groups[id] = ""
			}
		case xml.CharData:
			if text.Len() < 4096 {
				text.Write(t)
			}
		case xml.EndElement:
			name := t.Name.Local
			value := text.String()
			parent := ""
			if len(stack) >= 2 {
				parent = stack[len(stack)-2]
			}
			switch {
			case name == "npcommid" && parent == "trophyconf":
				out.npcommid = strings.TrimSpace(value)
			case name == "title-name" && parent == "trophyconf":
				out.title = value
			case name == "name" && parent == "trophy" && cur != nil:
				cur.name = value
			case name == "detail" && parent == "trophy" && cur != nil:
				cur.detail = value
			case name == "name" && parent == "group" && curGroup > 0:
				out.groups[curGroup] = value
			case name == "trophy" && cur != nil:
				out.trophies = append(out.trophies, *cur)
				cur = nil
			case name == "group":
				curGroup = -1
			}
			if len(stack) > 0 {
				stack = stack[:len(stack)-1]
			}
			text.Reset()
		case xml.Directive:
			return nil, ErrXML
		}
	}
	if len(out.trophies) == 0 || len(out.trophies) > 256 {
		return nil, ErrXML
	}
	sort.Slice(out.trophies, func(i, j int) bool { return out.trophies[i].id < out.trophies[j].id })
	for _, t := range out.trophies {
		if t.group != nil {
			if _, ok := out.groups[*t.group]; !ok {
				out.groups[*t.group] = ""
			}
		}
	}
	return out, nil
}

func trophyAttrs(el xml.StartElement) (*parsedTrophy, error) {
	t := &parsedTrophy{}
	id, ok := attrInt(el, "id")
	if !ok || id < 0 || id > 255 {
		return nil, ErrXML
	}
	t.id = id
	for _, a := range el.Attr {
		switch a.Name.Local {
		case "ttype":
			g, ok := grades[a.Value]
			if !ok {
				return nil, ErrXML
			}
			t.grade = g
		case "hidden":
			t.hidden = a.Value == "yes"
		case "gid":
			if a.Value != "" && a.Value != "000" {
				g, err := strconv.Atoi(a.Value)
				if err != nil || !idRe.MatchString(a.Value) || g < 1 || g > 99 {
					return nil, ErrXML
				}
				t.group = &g
			}
		}
	}
	if t.grade == "" {
		return nil, ErrXML
	}
	return t, nil
}

func attrInt(el xml.StartElement, name string) (int, bool) {
	for _, a := range el.Attr {
		if a.Name.Local == name {
			if !idRe.MatchString(a.Value) {
				return 0, false
			}
			n, err := strconv.Atoi(a.Value)
			return n, err == nil
		}
	}
	return 0, false
}

// readIcons adds TROPnnn.PNG, ICON0.PNG and GRnnn.PNG of trophies/groups the proposal declares.
// Files over 512 KiB or not starting with the PNG signature are left out (the server re-checks).
func readIcons(dir string, p *Proposal) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil // no icons is fine
	}
	trophies := map[int]bool{}
	for _, t := range p.Trophies {
		trophies[t.ID] = true
	}
	groups := map[int]bool{}
	for _, g := range p.Groups {
		groups[g.ID] = true
	}
	total := 0
	for _, e := range entries {
		m := iconRe.FindStringSubmatch(e.Name())
		if m == nil || !e.Type().IsRegular() {
			continue
		}
		var key string
		switch {
		case m[2] != "":
			id, _ := strconv.Atoi(m[2])
			if !trophies[id] {
				continue
			}
			key = strconv.Itoa(id)
		case m[3] != "":
			id, _ := strconv.Atoi(m[3])
			if !groups[id] {
				continue
			}
			key = "group-" + strconv.Itoa(id)
		default:
			key = "icon0"
		}
		data, err := source.ReadLimited(filepath.Join(dir, e.Name()), maxIconBytes)
		if err != nil || !bytes.HasPrefix(data, []byte("\x89PNG\r\n\x1a\n")) {
			continue
		}
		total += len(data)
		if total > maxIconsTotal {
			return fmt.Errorf("icons larger than %d bytes in total", maxIconsTotal)
		}
		p.Icons[key] = base64.StdEncoding.EncodeToString(data)
	}
	return nil
}
