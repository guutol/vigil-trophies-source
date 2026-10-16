package notify

import "sync"

// MaxInARow: more notifications than this waiting at once become one summary card.
const MaxInARow = 3

// Queue holds the cards waiting to be shown, one after the other. Safe for concurrent use.
type Queue struct {
	mu      sync.Mutex
	pending []Item
}

// Add queues items. When more than MaxInARow would be waiting (or a summary is already waiting),
// the cards not shown yet are merged into a single summary; platinum trophies are never merged
// (they always get their own card), and neither is an update notice.
func (q *Queue) Add(items ...Item) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.pending = append(q.pending, items...)
	waitingSummary := false
	for _, it := range q.pending {
		waitingSummary = waitingSummary || it.Summary
	}
	// A summary already waiting takes the new ones too, instead of cards after it.
	if len(q.pending) <= MaxInARow && !waitingSummary {
		return
	}
	var keep []Item
	summary := Item{Summary: true, Icon: GenericIcon(Gold)}
	for _, it := range q.pending {
		switch {
		case it.IsPlatinum() || it.Update:
			keep = append(keep, it)
		case it.Summary:
			summary.Count += it.Count
		default:
			summary.Count++
		}
	}
	if summary.Count > 0 {
		keep = append(keep, summary)
	}
	q.pending = keep
}

// AddInOrder queues items as they are, never merged (the "Test notification" sequence).
func (q *Queue) AddInOrder(items ...Item) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.pending = append(q.pending, items...)
}

// Next takes the next card; ok is false when nothing is waiting.
func (q *Queue) Next() (Item, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if len(q.pending) == 0 {
		return Item{}, false
	}
	it := q.pending[0]
	q.pending = q.pending[1:]
	return it, true
}

// Len is how many cards are waiting.
func (q *Queue) Len() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.pending)
}
