package app

import (
	"context"
	"errors"
	"log"
	"sync"

	"github.com/vigil-tracker/agent/internal/api"
	"github.com/vigil-tracker/agent/internal/config"
	"github.com/vigil-tracker/agent/internal/outbox"
	"github.com/vigil-tracker/agent/internal/source"
	"github.com/vigil-tracker/agent/internal/source/bbportlog"
	"github.com/vigil-tracker/agent/internal/syncer"
)

// State is what the background sync is doing.
type State int

const (
	Stopped      State = iota // not started yet, or paused by the user
	Running                   // watching the sources and sending
	NotLinked                 // no token: the user has to link this PC
	NoSources                 // linked, but nothing to watch
	Unauthorized              // the server refused the token (revoked from the website)
	Failed                    // could not start (e.g. a source folder disappeared); Err says why
)

// Status is a snapshot for the tray menu. Offline: while Running, the last send could not reach
// the server (events stay queued, Pending of them, and are retried with backoff).
type Status struct {
	State   State
	Paused  bool
	Err     error
	Offline bool
	Pending int
}

// Engine runs the same sync loop as "vigil-agent run" in the background, and can be paused,
// resumed and restarted (after linking or changing sources). Safe for concurrent use.
type Engine struct {
	Store *config.Store
	Log   *log.Logger
	// OnUnlocks receives unlocks the server accepted that were found while running.
	OnUnlocks func(events []outbox.Event)
	// OnChange is called (from any goroutine) whenever Status changes.
	OnChange func()

	mu      sync.Mutex
	state   State
	paused  bool
	err     error
	offline bool
	pending int
	cancel  context.CancelFunc
	done    chan struct{}
	stopped bool
}

// Status returns the current state.
func (e *Engine) Status() Status {
	e.mu.Lock()
	defer e.mu.Unlock()
	return Status{State: e.state, Paused: e.paused, Err: e.err, Offline: e.offline, Pending: e.pending}
}

// Restart stops the loop (if running) and starts it again with the current config, unless the
// user paused it.
func (e *Engine) Restart() {
	e.halt()
	e.mu.Lock()
	paused := e.paused
	e.mu.Unlock()
	if !paused {
		e.start()
	}
	e.changed()
}

// SetPaused pauses or resumes syncing. Unlocks are not lost while paused: the files stay on
// disk and are read again on resume.
func (e *Engine) SetPaused(paused bool) {
	e.mu.Lock()
	e.paused = paused
	e.mu.Unlock()
	e.Restart()
}

// Close stops the loop for good and waits for it.
func (e *Engine) Close() {
	e.mu.Lock()
	e.stopped = true
	e.mu.Unlock()
	e.halt()
}

func (e *Engine) halt() {
	e.mu.Lock()
	cancel, done := e.cancel, e.done
	e.cancel, e.done = nil, nil
	e.mu.Unlock()
	if cancel != nil {
		cancel()
		<-done
	}
	e.set(Stopped, nil)
}

func (e *Engine) start() {
	e.mu.Lock()
	if e.stopped || e.cancel != nil {
		e.mu.Unlock()
		return
	}
	e.mu.Unlock()

	s, state, err := e.build()
	if s == nil {
		e.set(state, err)
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	e.mu.Lock()
	e.cancel, e.done = cancel, done
	e.mu.Unlock()
	e.set(Running, nil)
	go func() {
		defer close(done)
		err := s.Run(ctx)
		if ctx.Err() != nil {
			return // halted on purpose
		}
		if errors.Is(err, api.ErrUnauthorized) {
			e.Log.Printf("the server rejected the token (revoked?); link this PC again")
			e.set(Unauthorized, err)
		} else {
			e.Log.Printf("sync stopped: %v", err)
			e.set(Failed, err)
		}
		e.mu.Lock()
		e.cancel, e.done = nil, nil
		e.mu.Unlock()
		e.changed()
	}()
}

// build prepares a syncer like "vigil-agent run" does, or says why it cannot.
func (e *Engine) build() (*syncer.Syncer, State, error) {
	token, err := e.Store.Token()
	if err != nil {
		return nil, Failed, err
	}
	if token == "" {
		return nil, NotLinked, nil
	}
	configs := e.Store.Sources()
	logPath := PlayLogPath(e.Store)
	// No folder to watch: nothing to do, unless trophies are still queued (the last folder was just
	// removed, or the server was offline). Then the loop runs with no sources until they are sent.
	if len(configs) == 0 && !FileExists(logPath) && queued(e.Store) == 0 {
		return nil, NoSources, nil
	}
	client, err := api.New(e.Store.Server(), token)
	if err != nil {
		return nil, Failed, err
	}
	srcs, err := OpenSources(configs, e.Log)
	if err != nil {
		return nil, Failed, err
	}
	if FileExists(logPath) {
		src, err := bbportlog.New(logPath, Identify(ConfiguredGameDir(e.Store), nil))
		if err != nil {
			return nil, Failed, err
		}
		srcs = append(srcs, source.Source(src))
	}
	s, err := syncer.New(client, e.Store, srcs, e.Log)
	if err != nil {
		return nil, Failed, err
	}
	s.Proposer = Proposer(client, e.Store, srcs, e.Log)
	s.OnDelivered = e.OnUnlocks
	s.OnConnection = e.connection
	e.Log.Printf("vigil-agent-tray %s, server %s, %d event(s) pending", api.Version, client.BaseURL, s.Pending())
	return s, Running, nil
}

func (e *Engine) set(state State, err error) {
	e.mu.Lock()
	e.state, e.err = state, err
	e.offline, e.pending = false, 0 // unknown again until the next send attempt
	e.mu.Unlock()
}

// connection records whether the last send reached the server; the menu changes only when it does.
func (e *Engine) connection(ok bool, pending int) {
	e.mu.Lock()
	changed := e.offline == ok || (!ok && e.pending != pending)
	e.offline, e.pending = !ok, pending
	e.mu.Unlock()
	if changed {
		if ok {
			e.Log.Printf("the server can be reached again")
		}
		e.changed()
	}
}

func (e *Engine) changed() {
	if e.OnChange != nil {
		e.OnChange()
	}
}

// queued is how many events wait in the outbox (0 when it cannot be read).
func queued(store *config.Store) int {
	o, err := outbox.Open(store.OutboxPath())
	if err != nil {
		return 0
	}
	return o.Len()
}
