package cluster

import (
	"context"
	"encoding/json"
	"log/slog"
	"sync"
	"time"

	"github.com/locklens/locklens/internal/domain/events"
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

// Bus carries messages between the servers sharing the store; publishing on it reaches the browsers of every server.
type Bus interface {
	// Publish delivers an event to this server's browsers and sends it to the
	// other servers, whose browsers receive it too.
	Publish(kind string, payload any)
	// Handle sets the handler of a message kind sent by other servers (instead
	// of delivering it to the browsers).
	Handle(kind string, fn func(json.RawMessage))
	// Send sends a message to the other servers (not to this one). It does not
	// block: messages are dropped when the queue is full or no other server
	// runs.
	Send(kind string, payload any)
	// Run sends queued messages and receives the others' until ctx ends.
	Run(ctx context.Context)
}

var _ Bus = (*bus)(nil)

// bus carries messages between the servers: browser events (published on
// the server that observed them, delivered to every server's browsers) and
// requests (for example "collect this target now", handled by its owner).
type bus struct {
	node  Node
	store Repositories
	log   *slog.Logger
	queue chan envelope
	// local receives the events published here and the events other
	// servers published (the browsers' event hub of this server).
	local events.Publisher

	mu       sync.RWMutex
	handlers map[string]func(json.RawMessage)
}

// NewBus returns the bus of a node; local receives the events for this
// server's browsers.
func NewBus(node Node, store Repositories, local events.Publisher, log *slog.Logger) Bus {
	return &bus{node: node, store: store, local: local, log: log, queue: make(chan envelope, 256), handlers: map[string]func(json.RawMessage){}}
}

// Publish delivers an event to this server's browsers and sends it to the
// other servers, whose browsers receive it too.
func (b *bus) Publish(kind string, payload any) {
	b.local.Publish(kind, payload)
	b.Send(kind, payload)
}

// Handle sets the handler of a message kind sent by other servers (instead
// of delivering it to the browsers).
func (b *bus) Handle(kind string, fn func(json.RawMessage)) {
	b.mu.Lock()
	b.handlers[kind] = fn
	b.mu.Unlock()
}

// Send sends a message to the other servers (not to this one). It does not
// block: messages are dropped when the queue is full or no other server
// runs.
func (b *bus) Send(kind string, payload any) {
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
func (b *bus) Run(ctx context.Context) {
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

func (b *bus) receive(ctx context.Context) {
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

func (b *bus) dispatch(payload string) {
	var e envelope
	if json.Unmarshal([]byte(payload), &e) != nil || e.From == b.node.ID() {
		return
	}
	b.mu.RLock()
	h := b.handlers[e.Kind]
	b.mu.RUnlock()
	if h != nil {
		h(e.Payload)
		return
	}
	b.local.Publish(e.Kind, e.Payload)
}
