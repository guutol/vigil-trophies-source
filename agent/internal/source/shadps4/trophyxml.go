package shadps4

import (
	"bytes"
	"encoding/xml"
	"errors"
	"io"
	"strconv"
	"strings"
	"time"
)

// MaxFileSize is far above any real trophy XML (tens of KiB).
const MaxFileSize = 4 << 20

// Errors for unusable files. Messages never include file content.
var (
	ErrDTD      = errors.New("XML with DOCTYPE/ENTITY declarations is not accepted")
	ErrNotXML   = errors.New("not a trophy XML file")
	ErrTrophyID = errors.New("invalid trophy id")
)

// Trophy is one <trophy> element.
type Trophy struct {
	ID       int
	Unlocked bool
	Time     time.Time // zero when missing or implausible
}

// File is what a trophy XML says: the optional <npcommid> and the trophies.
type File struct {
	NPCommID string
	Trophies []Trophy
}

// ParseTrophyXML reads the unlock state written by shadPS4:
//
//	<trophyconf> ... <npcommid>NPWR05818_00</npcommid> ...
//	  <trophy id="000" ... unlockstate="true" timestamp="1760077800"> ... </trophy>
//
// Only element and attribute names are trusted; DTDs and entity declarations are refused before
// parsing (no XXE), and the Go decoder never fetches anything.
func ParseTrophyXML(data []byte) (*File, error) {
	if len(data) > MaxFileSize {
		return nil, errors.New("trophy XML too large")
	}
	upper := bytes.ToUpper(data)
	if bytes.Contains(upper, []byte("<!DOCTYPE")) || bytes.Contains(upper, []byte("<!ENTITY")) {
		return nil, ErrDTD
	}
	dec := xml.NewDecoder(bytes.NewReader(data))
	dec.Strict = true
	dec.CharsetReader = func(label string, r io.Reader) (io.Reader, error) {
		if strings.EqualFold(label, "utf-8") {
			return r, nil
		}
		return nil, errors.New("unsupported XML encoding")
	}
	out := &File{}
	seen := map[int]bool{}
	depth := 0
	rootSeen := false
	inNPCommID := false
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, ErrNotXML
		}
		switch t := tok.(type) {
		case xml.StartElement:
			depth++
			if depth > 16 {
				return nil, ErrNotXML
			}
			if depth == 1 {
				rootSeen = true
			}
			switch t.Name.Local {
			case "npcommid":
				inNPCommID = true
			case "trophy":
				tr, err := trophyOf(t)
				if err != nil {
					return nil, err
				}
				if seen[tr.ID] {
					return nil, ErrTrophyID
				}
				seen[tr.ID] = true
				out.Trophies = append(out.Trophies, tr)
			}
		case xml.EndElement:
			depth--
			inNPCommID = false
		case xml.CharData:
			if inNPCommID && out.NPCommID == "" {
				out.NPCommID = strings.TrimSpace(string(t))
			}
		case xml.Directive:
			return nil, ErrDTD
		}
	}
	if !rootSeen {
		return nil, ErrNotXML
	}
	return out, nil
}

func trophyOf(el xml.StartElement) (Trophy, error) {
	var t Trophy
	idSeen := false
	var stamp string
	for _, a := range el.Attr {
		switch a.Name.Local {
		case "id":
			if len(a.Value) == 0 || len(a.Value) > 3 {
				return t, ErrTrophyID
			}
			n, err := strconv.Atoi(a.Value)
			if err != nil || n < 0 || n > 255 {
				return t, ErrTrophyID
			}
			t.ID, idSeen = n, true
		case "unlockstate":
			t.Unlocked = a.Value == "true"
		case "timestamp":
			stamp = a.Value
		}
	}
	if !idSeen {
		return t, ErrTrophyID
	}
	if t.Unlocked {
		t.Time = parseStamp(stamp)
	}
	return t, nil
}

// parseStamp reads unix seconds (what shadPS4 writes); milliseconds are tolerated.
func parseStamp(s string) time.Time {
	if len(s) == 0 || len(s) > 20 {
		return time.Time{}
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil || n <= 0 {
		return time.Time{}
	}
	if n > 100_000_000_000 { // more than year 5138 in seconds: must be milliseconds
		return time.UnixMilli(n).UTC()
	}
	return time.Unix(n, 0).UTC()
}
