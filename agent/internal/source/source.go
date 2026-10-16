// Package source defines where unlocks come from. Each emulator or port has its own package
// (bbport, rpcs3, shadps4) that only READS its files: sources never write, move or delete
// anything, and never touch decryption keys.
package source

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"regexp"
	"time"

	"github.com/vigil-tracker/agent/internal/uuid"
)

// Source names, also sent to the server.
const (
	BBPort    = "bbport"     // trophies.jsonl written by the Vigil patch
	BBPortLog = "bbport-log" // the official bbport's own output ("Runtime: trophy N unlocked")
	// GOW3Log: the output of the God of War III Remastered port (FelipePrado0/gow3_pc), the same
	// "Runtime: trophy N unlocked" lines in <saves folder>/last_run.log. Never Bloodborne.
	GOW3Log = "gow3-log"
	RPCS3   = "rpcs3"
	ShadPS4 = "shadps4"
)

// Types lists the supported source types.
var Types = []string{BBPort, BBPortLog, GOW3Log, RPCS3, ShadPS4}

// Patterns shared by every source; they match the server's validation.
var (
	TrophySetIDPattern = regexp.MustCompile(`^NPWR[0-9]{5}_[0-9]{2}$`)
	TitleIDPattern     = regexp.MustCompile(`^[A-Z0-9]{4,16}$`)
)

// Unlock is one unlocked trophy as a source sees it. TrophySetID, TitleID or both are set.
type Unlock struct {
	Source      string
	TrophySetID string
	TitleID     string
	TrophyID    int
	UnlockedAt  string // RFC 3339 UTC; "" when the source has no plausible time
}

// Source is something that can list unlocks. Scan returns every unlock it currently knows (the
// syncer drops the ones already queued); an error describes files that could not be read and
// may come together with the unlocks of the files that could.
type Source interface {
	Name() string
	Scan(ctx context.Context) ([]Unlock, error)
}

// Namespace for event ids: UUIDv5(NamespaceURL, "https://github.com/vigil-tracker/vigil/agent/events").
var Namespace = uuid.NewV5(uuid.NamespaceURL, "https://github.com/vigil-tracker/vigil/agent/events")

// EventID is the deterministic id of an unlock: UUIDv5 of "source|trophy_set_id-or-title_id|trophy_id".
// Re-reading the same file always gives the same ids, so the server can deduplicate retries.
func EventID(u Unlock) string {
	key := u.TrophySetID
	if key == "" {
		key = u.TitleID
	}
	return uuid.NewV5(Namespace, fmt.Sprintf("%s|%s|%d", u.Source, key, u.TrophyID)).String()
}

// Plausible time window for timestamps read from files; anything else is reported as "".
var (
	earliest = time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)
	latest   = time.Date(2100, 1, 1, 0, 0, 0, 0, time.UTC)
)

// FormatTime returns t as RFC 3339 UTC, or "" when it is outside 2000..2100.
func FormatTime(t time.Time) string {
	if t.Before(earliest) || !t.Before(latest) {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

// ErrTooLarge means a file is bigger than any real file of its kind.
var ErrTooLarge = errors.New("file too large")

// ReadLimited reads a whole file of at most limit bytes.
func ReadLimited(path string, limit int64) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, ErrTooLarge
	}
	return data, nil
}

// fileStamp identifies a version of a file.
type fileStamp struct {
	size  int64
	mtime time.Time
}

// Cache remembers what a file yielded, so a file is parsed again only when its size or
// modification time changes. Not safe for concurrent use.
type Cache struct {
	entries map[string]cacheEntry
}

type cacheEntry struct {
	stamp   fileStamp
	unlocks []Unlock
}

// Get returns the unlocks of path, calling parse only when the file changed since last time.
// A parse error is returned once and remembered as "no unlocks" until the file changes again.
// A missing file yields nothing and no error.
func (c *Cache) Get(path string, parse func(path string) ([]Unlock, error)) ([]Unlock, error) {
	if c.entries == nil {
		c.entries = map[string]cacheEntry{}
	}
	info, err := os.Stat(path)
	if errors.Is(err, fs.ErrNotExist) {
		delete(c.entries, path)
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%s: not a regular file", path)
	}
	stamp := fileStamp{size: info.Size(), mtime: info.ModTime()}
	if e, ok := c.entries[path]; ok && e.stamp == stamp {
		return e.unlocks, nil
	}
	unlocks, err := parse(path)
	if err != nil {
		unlocks = nil
		err = fmt.Errorf("%s: %w", path, err)
	}
	c.entries[path] = cacheEntry{stamp: stamp, unlocks: unlocks}
	return unlocks, err
}

// Paths returns the files currently cached (for status output and tests).
func (c *Cache) Paths() []string {
	out := make([]string, 0, len(c.entries))
	for p := range c.entries {
		out = append(out, p)
	}
	return out
}
