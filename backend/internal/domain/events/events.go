// Package events carries notices of what happened (a target was collected,
// an incident changed) to whoever listens: the browsers' event streams,
// notifications, and other servers. Events say what to refresh; they carry
// no data of their own.
package events

import (
	"encoding/json"
	"sync"
)

// Publisher sends an event to its listeners. Publishing never blocks.
type Publisher interface {
	// Publish sends an event of a kind (for example "incident.updated") with
	// a small JSON payload naming what changed.
	Publish(kind string, payload any)
}

// Subscriber lets a listener receive the events published on this server.
type Subscriber interface {
	// Subscribe returns a channel of events and a function that ends the
	// subscription; false when there are too many subscribers.
	Subscribe() (<-chan Event, func(), bool)
}

// Event is one published event with its payload as JSON.
type Event struct {
	Kind string
	Data []byte
}

// Fanout publishes every event to several publishers.
type Fanout []Publisher

// Publish implements Publisher.
func (f Fanout) Publish(kind string, payload any) {
	for _, p := range f {
		p.Publish(kind, payload)
	}
}

// Hub delivers the events published on this server to its subscribers. Each
// subscriber has a small buffer; a slow one misses events (the UI then
// refetches) instead of blocking the publisher.
type Hub struct {
	mu      sync.Mutex
	subs    map[chan Event]struct{}
	max     int
	onCount func(int)
}

var (
	_ Publisher  = (*Hub)(nil)
	_ Subscriber = (*Hub)(nil)
)

// NewHub returns a hub accepting at most max subscribers; onCount (may be
// nil) is told the number of subscribers when it changes.
func NewHub(max int, onCount func(int)) *Hub {
	return &Hub{subs: map[chan Event]struct{}{}, max: max, onCount: onCount}
}

// Publish implements Publisher.
func (h *Hub) Publish(kind string, payload any) {
	data, err := json.Marshal(payload)
	if err != nil {
		return
	}
	e := Event{Kind: kind, Data: data}
	h.mu.Lock()
	defer h.mu.Unlock()
	for c := range h.subs {
		select {
		case c <- e:
		default:
		}
	}
}

// Subscribe implements Subscriber.
func (h *Hub) Subscribe() (<-chan Event, func(), bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.subs) >= h.max {
		return nil, nil, false
	}
	c := make(chan Event, 32)
	h.subs[c] = struct{}{}
	h.count()
	return c, func() {
		h.mu.Lock()
		defer h.mu.Unlock()
		delete(h.subs, c)
		h.count()
	}, true
}

// count reports the number of subscribers; the caller holds h.mu.
func (h *Hub) count() {
	if h.onCount != nil {
		h.onCount(len(h.subs))
	}
}
