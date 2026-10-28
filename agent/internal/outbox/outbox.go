// Package outbox keeps the events waiting to be sent in outbox.jsonl.
//
// Every event gets its event_id once, when its line is read, and is written to disk before any
// send attempt. Retries therefore reuse the same event_id and the server can deduplicate them.
package outbox

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
)

// Event is one pending unlock, in the shape the API expects. Events queued by older agents
// have only title_id (and a source_end field, ignored now); they are still sent as they are.
type Event struct {
	EventID     string `json:"event_id"`
	Source      string `json:"source,omitempty"`
	TrophySetID string `json:"trophy_set_id,omitempty"`
	TitleID     string `json:"title_id,omitempty"`
	TrophyID    int    `json:"trophy_id"`
	UnlockedAt  string `json:"unlocked_at,omitempty"`
}

// Outbox is the persistent queue. Not safe for concurrent use.
type Outbox struct {
	path         string
	events       []Event
	corruptLines int
}

// Open loads path, or starts empty if it does not exist. Invalid JSONL records are skipped and counted.
func Open(path string) (*Outbox, error) {
	o := &Outbox{path: path}
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return o, nil
	}
	if err != nil {
		return nil, err
	}
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 64*1024), 1<<20) // Event lines should be small; allow ample room for older records.
	for sc.Scan() {
		var e Event
		if json.Unmarshal(sc.Bytes(), &e) == nil && e.EventID != "" {
			o.events = append(o.events, e)
		} else {
			o.corruptLines++
		}
	}
	return o, sc.Err()
}

// CorruptLines is the number of invalid JSONL records skipped while opening the outbox.
// Valid records are retained so recoverable events can still be sent.
func (o *Outbox) CorruptLines() int { return o.corruptLines }

// Len is the number of pending events.
func (o *Outbox) Len() int { return len(o.events) }

// Peek returns up to n events from the head, oldest first.
func (o *Outbox) Peek(n int) []Event {
	n = min(n, len(o.events))
	out := make([]Event, n)
	copy(out, o.events[:n])
	return out
}

// IDs returns the event ids in the queue.
func (o *Outbox) IDs() []string {
	out := make([]string, len(o.events))
	for i, e := range o.events {
		out[i] = e.EventID
	}
	return out
}

// Add appends events and persists the queue before returning.
func (o *Outbox) Add(events ...Event) error {
	if len(events) == 0 {
		return nil
	}
	old := o.events
	o.events = append(o.events, events...)
	if err := o.save(); err != nil {
		o.events = old
		return err
	}
	return nil
}

// Remove drops the events with the given ids and persists the queue.
func (o *Outbox) Remove(ids ...string) error {
	drop := make(map[string]bool, len(ids))
	for _, id := range ids {
		drop[id] = true
	}
	old := o.events
	kept := make([]Event, 0, len(old))
	for _, e := range old {
		if !drop[e.EventID] {
			kept = append(kept, e)
		}
	}
	o.events = kept
	if err := o.save(); err != nil {
		o.events = old
		return err
	}
	return nil
}

// save rewrites the file atomically (temp file + rename).
func (o *Outbox) save() error {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	for _, e := range o.events {
		if err := enc.Encode(e); err != nil {
			return err
		}
	}
	return WriteFileAtomic(o.path, buf.Bytes(), 0o600)
}

// WriteFileAtomic writes data to a temp file in the same directory, syncs it and renames it over path.
func WriteFileAtomic(path string, data []byte, perm os.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name) // no-op after a successful rename
	_ = tmp.Chmod(perm)   // best effort: Windows only has the read-only bit (CreateTemp already uses 0600)
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(name, path)
}
