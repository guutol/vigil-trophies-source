// Package syncer is the "run" loop: scan every source for unlocks, queue the new ones in the
// outbox, then send the outbox to the server, tolerating outages with backoff and Retry-After.
package syncer

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/vigil-tracker/agent/internal/api"
	"github.com/vigil-tracker/agent/internal/config"
	"github.com/vigil-tracker/agent/internal/outbox"
	"github.com/vigil-tracker/agent/internal/source"
)

const (
	// BatchSize is the server's per-request limit.
	BatchSize = 100
	// PollInterval is how often each source is checked. Sources only re-read files whose size
	// or modification time changed.
	PollInterval = 5 * time.Second
)

// Syncer owns the outbox and the set of already-queued event ids for a list of sources.
type Syncer struct {
	Client  *api.Client
	Store   *config.Store
	Outbox  *outbox.Outbox
	Sources []source.Source
	Log     *log.Logger
	Backoff *Backoff
	Now     func() time.Time
	// Interval between scans; PollInterval unless set (play mode uses a shorter one).
	Interval time.Duration
	// Proposer, when set, sends catalogs of unknown trophy sets (community catalog).
	Proposer *Proposer
	// OnDelivered, when set, is called after the server accepted events that were unlocked while
	// this syncer was running (found after its first scan): the tray app turns them into
	// notifications. The backlog of the first scan and events the server rejected are left out.
	OnDelivered func(events []outbox.Event)
	// OnConnection, when set, is called after every send attempt: ok is false while the server
	// cannot be reached (or answers with a temporary error), pending is what is still queued.
	// Nothing is dropped while ok is false; the tray app shows it instead of "syncing".
	OnConnection func(ok bool, pending int)

	known     map[string]bool
	lastError map[string]string // per source, so a broken file is reported once, not every tick
	nextSend  time.Time
	scans     int             // completed scans; events found after the first one are "live"
	live      map[string]bool // live event ids not delivered yet
}

// New prepares a syncer, recovering the outbox and the known ids from disk.
func New(client *api.Client, store *config.Store, sources []source.Source, logger *log.Logger) (*Syncer, error) {
	ob, err := outbox.Open(store.OutboxPath())
	if err != nil {
		return nil, fmt.Errorf("cannot open outbox: %w", err)
	}
	s := &Syncer{Client: client, Store: store, Outbox: ob, Sources: sources, Log: logger,
		Backoff: NewBackoff(), Now: time.Now, known: store.Known(), lastError: map[string]string{}, live: map[string]bool{}}
	// Crash recovery: anything in the outbox counts as known even if known.json was not saved.
	for _, id := range ob.IDs() {
		s.known[id] = true
	}
	return s, nil
}

// Run loops until ctx is cancelled (returns nil) or the token is rejected (returns api.ErrUnauthorized).
func (s *Syncer) Run(ctx context.Context) error {
	interval := s.Interval
	if interval <= 0 {
		interval = PollInterval
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		if err := s.Scan(ctx); err != nil {
			s.Log.Printf("error: %v", err)
		}
		if err := s.Flush(ctx); err != nil {
			return err
		}
		if s.Proposer != nil {
			if err := s.Proposer.Tick(ctx); err != nil {
				return err
			}
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

// Scan asks every source for its unlocks and queues the ones not seen before. The outbox is on
// disk before the known ids are saved; event ids are deterministic, so nothing is renumbered.
// Read problems are logged per source; only a failure to save the queue is returned.
func (s *Syncer) Scan(ctx context.Context) error {
	first := s.scans == 0
	s.scans++
	var events []outbox.Event
	for _, src := range s.Sources {
		unlocks, err := src.Scan(ctx)
		s.report(src.Name(), err)
		for _, u := range unlocks {
			id := source.EventID(u)
			if s.known[id] {
				continue
			}
			s.known[id] = true
			if !first {
				s.live[id] = true
			}
			events = append(events, outbox.Event{EventID: id, Source: u.Source, TrophySetID: u.TrophySetID,
				TitleID: u.TitleID, TrophyID: u.TrophyID, UnlockedAt: u.UnlockedAt})
		}
	}
	if len(events) == 0 {
		return nil
	}
	if err := s.Outbox.Add(events...); err != nil {
		return fmt.Errorf("cannot save outbox: %w", err)
	}
	s.Log.Printf("queued %d new unlock(s)", len(events))
	if err := s.Store.SetKnown(s.known); err != nil {
		return fmt.Errorf("cannot save known ids: %w", err)
	}
	return nil
}

func (s *Syncer) report(name string, err error) {
	msg := ""
	if err != nil && !errors.Is(err, context.Canceled) {
		msg = err.Error()
	}
	if msg != "" && msg != s.lastError[name] {
		s.Log.Printf("%s: some files could not be read: %s", name, msg)
	}
	s.lastError[name] = msg
}

// Flush sends the outbox in batches until it is empty or the server asks to wait.
func (s *Syncer) Flush(ctx context.Context) error {
	for s.Outbox.Len() > 0 && !s.Now().Before(s.nextSend) && ctx.Err() == nil {
		batch := s.Outbox.Peek(BatchSize)
		res, err := s.Client.SendUnlocks(ctx, batch)

		var retry *api.RetryableError
		var rejected *api.RejectedError
		switch {
		case err == nil:
			s.Backoff.Reset()
			for _, r := range res.Rejected {
				s.Log.Printf("server rejected event %s: %s", r.EventID, r.Reason)
			}
			if err := s.Outbox.Remove(ids(batch)...); err != nil {
				return fmt.Errorf("cannot save outbox: %w", err)
			}
			s.delivered(batch, res.Rejected)
			s.connection(true)
			s.Log.Printf("sent %d event(s): %d new, %d already known, %d waiting for a catalog, %d rejected",
				len(batch), res.Accepted, res.Duplicates, res.Pending, len(res.Rejected))
		case errors.Is(err, api.ErrUnauthorized):
			return err
		case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
			return nil
		case errors.As(err, &retry):
			s.wait(retry.RetryAfter, retry.Error())
			s.connection(false)
			return nil
		case errors.As(err, &rejected) && rejected.Status == http.StatusBadRequest:
			// The batch itself is malformed; retrying it would fail forever.
			s.Log.Printf("server refused a batch of %d event(s) (%s); dropping it", len(batch), rejected.Code)
			if err := s.Outbox.Remove(ids(batch)...); err != nil {
				return fmt.Errorf("cannot save outbox: %w", err)
			}
		default:
			s.wait(0, err.Error())
			s.connection(false)
			return nil
		}
	}
	return nil
}

// delivered reports the live events of a sent batch that the server did not reject.
func (s *Syncer) delivered(batch []outbox.Event, rejected []api.Rejected) {
	refused := map[string]bool{}
	for _, r := range rejected {
		refused[r.EventID] = true
	}
	var fresh []outbox.Event
	for _, e := range batch {
		if s.live[e.EventID] {
			delete(s.live, e.EventID)
			if !refused[e.EventID] {
				fresh = append(fresh, e)
			}
		}
	}
	if len(fresh) > 0 && s.OnDelivered != nil {
		s.OnDelivered(fresh)
	}
}

func (s *Syncer) connection(ok bool) {
	if s.OnConnection != nil {
		s.OnConnection(ok, s.Outbox.Len())
	}
}

// Pending is the number of events not yet delivered.
func (s *Syncer) Pending() int { return s.Outbox.Len() }

func (s *Syncer) wait(hint time.Duration, reason string) {
	d := hint
	if d <= 0 {
		d = s.Backoff.Next()
	}
	s.nextSend = s.Now().Add(d)
	s.Log.Printf("send failed (%s); %d event(s) kept, retrying in %s", reason, s.Outbox.Len(), d.Round(time.Second))
}

func ids(events []outbox.Event) []string {
	out := make([]string, len(events))
	for i, e := range events {
		out[i] = e.EventID
	}
	return out
}
