// Package trophyfile reads the trophies.jsonl file written by the bbport patch
// (format v1, see bbport-patch/FORMAT.md).
package trophyfile

import (
	"bytes"
	"encoding/json"
	"errors"
	"regexp"
	"strconv"
	"time"
)

// FileName is the file the patch appends to, inside the bbport user directory.
const FileName = "trophies.jsonl"

// MaxLineLength bounds a single line; real lines are under 100 bytes.
const MaxLineLength = 4096

var titleIDPattern = regexp.MustCompile(`^[A-Z0-9]{4,16}$`)

// Unlock is one valid line of trophies.jsonl.
type Unlock struct {
	TitleID    string
	TrophyID   int
	UnlockedAt string // RFC 3339, UTC, as written by the game; informative only
}

// Errors returned by ParseLine. Messages never include the line content.
var (
	ErrNotJSON     = errors.New("line is not a JSON object")
	ErrVersion     = errors.New("unsupported format version")
	ErrTitleID     = errors.New("invalid title_id")
	ErrTrophyID    = errors.New("invalid trophy_id")
	ErrUnlockedAt  = errors.New("invalid unlocked_at")
	ErrLineTooLong = errors.New("line too long")
)

type rawLine struct {
	V          json.RawMessage `json:"v"`
	TitleID    *string         `json:"title_id"`
	TrophyID   json.RawMessage `json:"trophy_id"`
	UnlockedAt *string         `json:"unlocked_at"`
}

var integerPattern = regexp.MustCompile(`^-?[0-9]{1,6}$`)

// integer accepts a bare JSON integer only (no strings, fractions or exponents).
func integer(raw json.RawMessage) (int, bool) {
	if !integerPattern.Match(raw) {
		return 0, false
	}
	n, err := strconv.Atoi(string(raw))
	return n, err == nil
}

// ParseLine validates one line (without the trailing newline) with the same rules as the patch.
// Unknown fields are ignored.
func ParseLine(line []byte) (Unlock, error) {
	line = bytes.TrimRight(line, "\r")
	if len(line) > MaxLineLength {
		return Unlock{}, ErrLineTooLong
	}
	dec := json.NewDecoder(bytes.NewReader(line))
	var raw rawLine
	if err := dec.Decode(&raw); err != nil || dec.More() {
		return Unlock{}, ErrNotJSON
	}
	if v, ok := integer(raw.V); !ok || v != 1 {
		return Unlock{}, ErrVersion
	}
	if raw.TitleID == nil || !titleIDPattern.MatchString(*raw.TitleID) {
		return Unlock{}, ErrTitleID
	}
	id, ok := integer(raw.TrophyID)
	if !ok || id < 0 || id > 255 {
		return Unlock{}, ErrTrophyID
	}
	if raw.UnlockedAt == nil {
		return Unlock{}, ErrUnlockedAt
	}
	if _, err := time.Parse(time.RFC3339, *raw.UnlockedAt); err != nil {
		return Unlock{}, ErrUnlockedAt
	}
	return Unlock{TitleID: *raw.TitleID, TrophyID: id, UnlockedAt: *raw.UnlockedAt}, nil
}
