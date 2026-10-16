package bbportlog

import (
	"bytes"
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"regexp"

	"github.com/vigil-tracker/agent/internal/source"
)

// DefaultTitleID is what the bbport patch also falls back to: bbport runs Bloodborne.
const DefaultTitleID = "CUSA03173"

const (
	maxSFOBytes    = 64 << 10
	maxNPBindBytes = 1 << 20
)

var npwrInBytes = regexp.MustCompile(`NPWR[0-9]{5}_[0-9]{2}`)

// Game is what the game folder says about itself; either field may be empty.
type Game struct {
	TrophySetID string // from sce_sys/npbind.dat
	TitleID     string // from sce_sys/param.sfo
}

// Identify reads <gameDir>/sce_sys/param.sfo and npbind.dat (both plain, not encrypted).
// Missing files are not errors; an unusable file is.
func Identify(gameDir string) (Game, error) {
	var g Game
	var errs []error
	if data, err := source.ReadLimited(filepath.Join(gameDir, "sce_sys", "param.sfo"), maxSFOBytes); err == nil {
		title, err := TitleIDFromSFO(data)
		if err != nil {
			errs = append(errs, err)
		}
		g.TitleID = title
	} else if !errors.Is(err, os.ErrNotExist) {
		errs = append(errs, err)
	}
	if data, err := source.ReadLimited(filepath.Join(gameDir, "sce_sys", "npbind.dat"), maxNPBindBytes); err == nil {
		g.TrophySetID = NPCommIDFromNPBind(data)
	} else if !errors.Is(err, os.ErrNotExist) {
		errs = append(errs, err)
	}
	return g, errors.Join(errs...)
}

// NPCommIDFromNPBind returns the only trophy set id in npbind.dat, or "" when there is none or
// more than one.
func NPCommIDFromNPBind(data []byte) string {
	found := map[string]bool{}
	for _, m := range npwrInBytes.FindAll(data, 16) {
		found[string(m)] = true
	}
	if len(found) != 1 {
		return ""
	}
	for id := range found {
		return id
	}
	return ""
}

var errSFO = errors.New("param.sfo is not valid")

// TitleIDFromSFO reads the TITLE_ID string of a param.sfo (little-endian PSF). Every offset is
// checked against the data; a bad file gives an error, never a panic.
func TitleIDFromSFO(data []byte) (string, error) {
	le := binary.LittleEndian
	if len(data) < 20 || !bytes.Equal(data[0:4], []byte("\x00PSF")) {
		return "", errSFO
	}
	keyTable, dataTable, count := le.Uint32(data[8:12]), le.Uint32(data[12:16]), le.Uint32(data[16:20])
	if count > 256 || uint64(20)+uint64(count)*16 > uint64(len(data)) {
		return "", errSFO
	}
	for i := uint32(0); i < count; i++ {
		e := data[20+i*16 : 36+i*16]
		keyOff, length, dataOff := le.Uint16(e[0:2]), le.Uint32(e[4:8]), le.Uint32(e[12:16])
		start := uint64(keyTable) + uint64(keyOff)
		if start >= uint64(len(data)) {
			return "", errSFO
		}
		end := bytes.IndexByte(data[start:], 0)
		if end < 0 || string(data[start:start+uint64(end)]) != "TITLE_ID" {
			continue
		}
		vStart := uint64(dataTable) + uint64(dataOff)
		vEnd := vStart + uint64(length)
		if length > 64 || vEnd > uint64(len(data)) {
			return "", errSFO
		}
		title := string(bytes.TrimRight(data[vStart:vEnd], "\x00"))
		if !source.TitleIDPattern.MatchString(title) {
			return "", errSFO
		}
		return title, nil
	}
	return "", nil
}
