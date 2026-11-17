// Package events is a tiny in-process pub/sub used to fan run events out to SSE
// subscribers. Events are also persisted to Postgres (the durable journal); the
// bus is only for live push to connected clients. Single-process, local-first.
package events

import (
	"encoding/json"
	"sync"
)

// Event is a live run event mirrored from the journal.
type Event struct {
	RunID string          `json:"runId"`
	Seq   int64           `json:"seq"`
	Type  string          `json:"type"`
	Node  string          `json:"node,omitempty"`
	Level string          `json:"level,omitempty"`
	Data  json.RawMessage `json:"data,omitempty"`
}

// Bus is a concurrency-safe per-run fan-out.
type Bus struct {
	mu   sync.RWMutex
	subs map[string]map[chan Event]struct{}
}

// NewBus creates an empty Bus.
func NewBus() *Bus {
	return &Bus{subs: make(map[string]map[chan Event]struct{})}
}

// Subscribe returns a buffered channel of events for a run plus an unsubscribe
// func the caller must invoke when done.
func (b *Bus) Subscribe(runID string) (<-chan Event, func()) {
	ch := make(chan Event, 64)
	b.mu.Lock()
	if b.subs[runID] == nil {
		b.subs[runID] = make(map[chan Event]struct{})
	}
	b.subs[runID][ch] = struct{}{}
	b.mu.Unlock()

	return ch, func() {
		b.mu.Lock()
		if set, ok := b.subs[runID]; ok {
			if _, ok := set[ch]; ok {
				delete(set, ch)
				close(ch)
			}
			if len(set) == 0 {
				delete(b.subs, runID)
			}
		}
		b.mu.Unlock()
	}
}

// Publish delivers an event to all subscribers of its run. Non-blocking: if a
// subscriber's buffer is full the event is dropped for that subscriber (it can
// always backfill from the durable journal).
func (b *Bus) Publish(e Event) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	for ch := range b.subs[e.RunID] {
		select {
		case ch <- e:
		default:
		}
	}
}
