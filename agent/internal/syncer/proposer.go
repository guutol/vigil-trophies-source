package syncer

import (
	"context"
	"errors"
	"log"
	"net/http"
	"sort"
	"time"

	"github.com/vigil-tracker/agent/internal/api"
	"github.com/vigil-tracker/agent/internal/catalog"
	"github.com/vigil-tracker/agent/internal/config"
)

const (
	// ProposeEvery is how often the agent looks for trophy sets the server does not know.
	ProposeEvery = 10 * time.Minute
	// maxProposalsPerRound stays under the server's limit (10 per minute).
	maxProposalsPerRound = 5
	maxStatusBatch       = 100
)

// Proposer sends catalog proposals for trophy sets the server does not know yet, built from the
// plain trophy data emulators keep (see package catalog). Each set is handled once: proposed,
// already known, or refused by the server; the result is remembered in catalogs.json.
type Proposer struct {
	Client    *api.Client
	Store     *config.Store
	Providers []catalog.Provider
	Log       *log.Logger
	Now       func() time.Time

	handled map[string]bool
	next    time.Time
}

// NewProposer loads the sets already handled.
func NewProposer(client *api.Client, store *config.Store, providers []catalog.Provider, logger *log.Logger) *Proposer {
	return &Proposer{Client: client, Store: store, Providers: providers, Log: logger, Now: time.Now,
		handled: store.HandledCatalogs()}
}

// Tick does one round when it is time; only api.ErrUnauthorized is returned.
func (p *Proposer) Tick(ctx context.Context) error {
	now := p.Now()
	if now.Before(p.next) {
		return nil
	}
	p.next = now.Add(ProposeEvery)

	dirs := map[string]catalog.Dir{}
	for _, prov := range p.Providers {
		for _, d := range prov.CatalogDirs() {
			if !p.handled[d.SetID] {
				dirs[d.SetID] = d
			}
		}
	}
	if len(dirs) == 0 {
		return nil
	}
	ids := make([]string, 0, len(dirs))
	for id := range dirs {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	var candidates []string
	for start := 0; start < len(ids); start += maxStatusBatch {
		batch := ids[start:min(start+maxStatusBatch, len(ids))]
		status, err := p.Client.CatalogStatus(ctx, batch)
		if errors.Is(err, api.ErrUnauthorized) {
			return err
		}
		if err != nil {
			p.Log.Printf("catalog: cannot ask the server (%v); trying again later", err)
			return nil
		}
		skip := map[string]bool{}
		for _, id := range append(status.Known, status.Proposed...) {
			skip[id] = true
			p.handled[id] = true
		}
		for _, id := range batch {
			if !skip[id] {
				candidates = append(candidates, id)
			}
		}
	}

	sent := 0
	for _, id := range candidates {
		if sent >= maxProposalsPerRound || ctx.Err() != nil {
			break
		}
		proposal, err := catalog.Build(dirs[id])
		if err != nil {
			p.Log.Printf("catalog: %s cannot be proposed: %v", id, err)
			p.handled[id] = true
			continue
		}
		status, err := p.Client.ProposeCatalog(ctx, proposal)
		var rejected *api.RejectedError
		switch {
		case err == nil:
			p.Log.Printf("catalog: proposed %s (%s)", id, status)
			p.handled[id] = true
			sent++
		case errors.Is(err, api.ErrUnauthorized):
			p.save()
			return err
		case errors.As(err, &rejected) && rejected.Status != http.StatusTooManyRequests:
			p.Log.Printf("catalog: the server refused %s (%s)", id, rejected.Code)
			p.handled[id] = true
		default:
			p.Log.Printf("catalog: cannot send %s now (%v); trying again later", id, err)
			p.save()
			return nil
		}
	}
	p.save()
	return nil
}

func (p *Proposer) save() {
	if err := p.Store.SetHandledCatalogs(p.handled); err != nil {
		p.Log.Printf("catalog: cannot save catalogs.json: %v", err)
	}
}
