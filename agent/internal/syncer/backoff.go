package syncer

import (
	"math/rand/v2"
	"time"
)

// Backoff is exponential with jitter: Min, 2*Min, 4*Min... capped at Max, each randomised
// into [d/2, d] so many agents coming back after an outage do not retry in lockstep.
type Backoff struct {
	Min, Max time.Duration
	attempt  int
	rand     func() float64
}

// NewBackoff returns the agent's default: 1 s up to 5 min.
func NewBackoff() *Backoff {
	return &Backoff{Min: time.Second, Max: 5 * time.Minute, rand: rand.Float64}
}

// Next returns the delay before the next attempt and advances the sequence.
func (b *Backoff) Next() time.Duration {
	d := b.Max
	if b.attempt < 30 {
		if step := b.Min << b.attempt; step > 0 && step < b.Max {
			d = step
		}
	}
	b.attempt++
	r := b.rand
	if r == nil {
		r = rand.Float64
	}
	return d/2 + time.Duration(r()*float64(d/2))
}

// Reset starts over after a success.
func (b *Backoff) Reset() { b.attempt = 0 }
