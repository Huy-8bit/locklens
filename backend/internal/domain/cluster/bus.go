package cluster

import (
	"context"
	"encoding/json"
	"log/slog"
	"sync"
	"time"
)

// channel is the NOTIFY channel of the bus.
const channel = "locklens_bus"

// maxMessage stays below PostgreSQL's NOTIFY payload limit (8000 bytes);
// larger messages are not sent to other servers.
const maxMessage = 7900

type envelope struct {
	From    string          `json:"from"`
	Kind    string          `json:"kind"`
	Payload json.RawMessage `json:"payload"`
}

// Bus carries messages between the servers: browser events (published on
// the server that observed them, delivered to every server's browsers) and
// requests (for example "collect this target now", handled by its owner).
type Bus struct {
	node  *Node
	store Store
	log   *slog.Logger
	queue chan envelope

	mu       sync.RWMutex
	local    func(kind string, payload any)
	handlers map[string]func(json.RawMessage)
}

// NewBus returns the bus of a node.
func NewBus(node *Node, store Store, log *slog.Logger) *Bus {
	return &Bus{node: node, store: store, log: log, queue: make(chan envelope, 256), handlers: map[string]func(json.RawMessage){}}
}

// DeliverTo sets where messages without a handler go on this server (the
// browsers' event hub).
func (b *Bus) DeliverTo(local func(kind string, payload any)) {
	b.mu.Lock()
	b.local = local
	b.mu.Unlock()
}

// Handle sets the handler of a message kind sent by other servers.
func (b *Bus) Handle(kind string, fn func(json.RawMessage)) {
	b.mu.Lock()
	b.handlers[kind] = fn
	b.mu.Unlock()
}

// Send sends a message to the other servers (not to this one). It does not
// block: messages are dropped when the queue is full or no other server
// runs.
func (b *Bus) Send(kind string, payload any) {
	if b.node.Alone() {
		return
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return
	}
	select {
	case b.queue <- envelope{From: b.node.ID(), Kind: kind, Payload: raw}:
	default:
		b.log.Warn("cluster bus: queue full, message dropped", "kind", kind)
	}
}

// Run sends queued messages and receives the others' until ctx ends.
func (b *Bus) Run(ctx context.Context) {
	go b.receive(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case e := <-b.queue:
			raw, err := json.Marshal(e)
			if err != nil || len(raw) > maxMessage {
				continue
			}
			nctx, cancel := context.WithTimeout(ctx, 5*time.Second)
			if err := b.store.Notify(nctx, channel, string(raw)); err != nil && ctx.Err() == nil {
				b.log.Warn("cluster bus: send failed", "kind", e.Kind, "error", err.Error())
			}
			cancel()
		}
	}
}

func (b *Bus) receive(ctx context.Context) {
	for ctx.Err() == nil {
		err := b.store.Listen(ctx, channel, b.dispatch)
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			b.log.Warn("cluster bus: listening failed; retrying", "error", err.Error())
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(2 * time.Second):
		}
	}
}

func (b *Bus) dispatch(payload string) {
	var e envelope
	if json.Unmarshal([]byte(payload), &e) != nil || e.From == b.node.ID() {
		return
	}
	b.mu.RLock()
	h, local := b.handlers[e.Kind], b.local
	b.mu.RUnlock()
	switch {
	case h != nil:
		h(e.Payload)
	case local != nil:
		local(e.Kind, e.Payload)
	}
}
