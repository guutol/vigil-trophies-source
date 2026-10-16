package bbportlog

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"io"
	"io/fs"
	"os"
	"time"
)

const (
	// markSize is how many bytes right before the read offset are remembered: if they change,
	// the file was rewritten (the launcher recreates last_run.log on every run) and is read again.
	markSize = 256
	// MaxLineLength bounds one line; longer stretches are skipped.
	MaxLineLength = 4096
	// maxRead bounds one read.
	maxRead = 1 << 20
)

// Tail reads the complete lines appended to a text file, noticing when the file is truncated,
// rewritten or replaced. It only reads.
type Tail struct {
	Path string

	offset int64
	size   int64
	mtime  time.Time
	mark   [32]byte
	marked bool
	skip   bool // inside an over-long line: drop bytes until the next newline
}

// Lines returns the complete lines added since the last call (without "\n" or "\r\n") and
// whether reading restarted from the beginning. A missing file gives no lines and no error.
func (t *Tail) Lines() (lines []string, reset bool, err error) {
	f, err := os.Open(t.Path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, false, err
	}
	if info.Size() == t.size && info.ModTime().Equal(t.mtime) {
		return nil, false, nil // unchanged
	}
	if t.offset > 0 && !t.sameMark(f, info.Size()) {
		reset = true
		t.offset, t.skip = 0, false
	}
	for {
		n := min(info.Size()-t.offset, maxRead)
		if n <= 0 {
			break
		}
		chunk := make([]byte, n)
		if _, err := f.ReadAt(chunk, t.offset); err != nil && !errors.Is(err, io.EOF) {
			return lines, reset, err
		}
		last := bytes.LastIndexByte(chunk, '\n')
		if last < 0 {
			if n > MaxLineLength { // no newline in a long stretch: skip it
				t.offset += n
				t.skip = true
				continue
			}
			break // a line still being written
		}
		for _, raw := range bytes.Split(chunk[:last], []byte{'\n'}) {
			if t.skip {
				t.skip = false
				continue
			}
			if len(raw) <= MaxLineLength {
				// "\r\n", and also "\r\r\n": depending on how the launcher started the game, its
				// Windows text-mode file turns the game's own "\r\n" into "\r\r\n".
				lines = append(lines, string(bytes.TrimRight(raw, "\r")))
			}
		}
		t.offset += int64(last + 1)
	}
	t.size, t.mtime = info.Size(), info.ModTime()
	t.remember(f)
	return lines, reset, nil
}

// sameMark reports whether the bytes before the offset are still the ones read last time.
func (t *Tail) sameMark(f *os.File, size int64) bool {
	if size < t.offset || !t.marked {
		return false
	}
	sum, ok := markOf(f, t.offset)
	return ok && sum == t.mark
}

func (t *Tail) remember(f *os.File) {
	t.mark, t.marked = markOf(f, t.offset)
}

func markOf(f *os.File, offset int64) ([32]byte, bool) {
	start := max(0, offset-markSize)
	buf := make([]byte, offset-start)
	if _, err := f.ReadAt(buf, start); err != nil && !errors.Is(err, io.EOF) {
		return [32]byte{}, false
	}
	return sha256.Sum256(buf), true
}
