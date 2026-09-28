// Package events is a small in-process publish/subscribe broker feeding the
// server-sent event stream.
package events

import "sync"

// Event is one message on the stream.
type Event struct {
	Type string `json:"type"`
	Data any    `json:"data"`
}

// Broker fans events out to subscribers. Slow subscribers drop events
// rather than blocking publishers.
type Broker struct {
	mu   sync.Mutex
	subs map[chan Event]struct{}
}

// New creates a broker.
func New() *Broker { return &Broker{subs: map[chan Event]struct{}{}} }

// Subscribe returns a channel of events and a function to unsubscribe.
func (b *Broker) Subscribe() (<-chan Event, func()) {
	ch := make(chan Event, 64)
	b.mu.Lock()
	b.subs[ch] = struct{}{}
	b.mu.Unlock()
	return ch, func() {
		b.mu.Lock()
		if _, ok := b.subs[ch]; ok {
			delete(b.subs, ch)
			close(ch)
		}
		b.mu.Unlock()
	}
}

// Publish sends ev to every subscriber without blocking.
func (b *Broker) Publish(ev Event) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for ch := range b.subs {
		select {
		case ch <- ev:
		default:
		}
	}
}

// Changed publishes a "changed" event for data topics (images, tags,
// metrics, runs, trash, dedup, all) so clients can refetch.
func (b *Broker) Changed(topics ...string) {
	b.Publish(Event{Type: "changed", Data: map[string]any{"topics": topics}})
}
