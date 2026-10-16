package trophyfile

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"io/fs"
	"os"
)

// headSize is how many leading bytes identify the file, to notice it was recreated.
const headSize = 64

// Position is how far the file has been processed. It is saved in state.json.
type Position struct {
	Path     string `json:"path"`
	Offset   int64  `json:"offset"`
	Size     int64  `json:"size"`
	HeadLen  int    `json:"head_len"`
	HeadHash string `json:"head_hash"`
}

// Entry is a valid line together with the file offset right after its newline.
type Entry struct {
	Unlock Unlock
	End    int64
}

// Result of one ReadNew call.
type Result struct {
	Entries  []Entry
	Invalid  []error  // one per skipped line; never contains line content
	Position Position // where the next read starts
	Reset    bool     // the file was truncated or recreated, reading restarted at 0
}

// ReadNew reads the complete lines appended to path since pos, at most maxBytes per call.
// A missing file is not an error: the game may not have unlocked anything yet.
func ReadNew(path string, pos Position, maxBytes int64) (Result, error) {
	res := Result{Position: pos}
	f, err := os.Open(path)
	if errors.Is(err, fs.ErrNotExist) {
		return res, nil
	}
	if err != nil {
		return res, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return res, err
	}
	size := info.Size()

	head := make([]byte, min(int64(headSize), size))
	if _, err := io.ReadFull(f, head); err != nil {
		return res, err
	}
	if pos.Path != path || truncated(pos, size, head) {
		res.Reset = pos.Path == path && (pos.Offset > 0 || pos.HeadLen > 0)
		pos = Position{Path: path}
	}

	if size > pos.Offset {
		n := min(size-pos.Offset, maxBytes)
		chunk := make([]byte, n)
		if _, err := f.ReadAt(chunk, pos.Offset); err != nil && !errors.Is(err, io.EOF) {
			return res, err
		}
		last := bytes.LastIndexByte(chunk, '\n')
		switch {
		case last >= 0:
			res.Entries, res.Invalid = parseLines(chunk[:last+1], pos.Offset)
			pos.Offset += int64(last + 1)
		case n > MaxLineLength:
			// No newline in a long stretch: not something the patch writes. Skip it.
			res.Invalid = append(res.Invalid, ErrLineTooLong)
			pos.Offset += n
		}
	}

	pos.Size = size
	pos.HeadLen = len(head)
	pos.HeadHash = hashOf(head)
	res.Position = pos
	return res, nil
}

func truncated(pos Position, size int64, head []byte) bool {
	if size < pos.Offset || size < int64(pos.HeadLen) {
		return true
	}
	return pos.HeadLen > 0 && hashOf(head[:pos.HeadLen]) != pos.HeadHash
}

func parseLines(data []byte, base int64) ([]Entry, []error) {
	var entries []Entry
	var invalid []error
	start := 0
	for start < len(data) {
		end := bytes.IndexByte(data[start:], '\n') + start
		line := data[start:end]
		if len(bytes.TrimSpace(line)) > 0 {
			u, err := ParseLine(line)
			if err != nil {
				invalid = append(invalid, err)
			} else {
				entries = append(entries, Entry{Unlock: u, End: base + int64(end+1)})
			}
		}
		start = end + 1
	}
	return entries, invalid
}

func hashOf(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
