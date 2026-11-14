package cluster

import (
	"context"
	"encoding/json"
	"log/slog"
	"slices"
	"sync"
	"time"

	"github.com/locklens/locklens/internal/domain/events"
	"github.com/locklens/locklens/internal/lib/obs"
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
	node    Node
	store   Repositories
	log     *slog.Logger
	metrics *obs.Metrics
	queue   chan envelope
	// local receives the events published here and the events other
	// servers published (the browsers' event hub of this server).
	local events.Publisher

	mu       sync.RWMutex
	handlers map[string]func(json.RawMessage)

	// Collection events are coalesced per target before they are sent to
	// the other servers (see coalesceKind).
	cmu       sync.Mutex
	collected map[int64]*pendingCollected
	now       func() time.Time
}

// coalesceKind is sent to the other servers at most once per second per
// target: a server collecting hundreds of targets would otherwise send one
// NOTIFY per collector run. The merged message lists every collector that
// ran ("collectors"); this server's own browsers still get every event.
const (
	coalesceKind  = "target.collected"
	coalesceEvery = time.Second
)

// collectedEvent is the payload of coalesceKind.
type collectedEvent struct {
	TargetID   int64     `json:"target_id"`
	Collector  string    `json:"collector"`
	Collectors []string  `json:"collectors,omitempty"`
	At         time.Time `json:"at"`
	OK         bool      `json:"ok"`
}

type pendingCollected struct {
	lastSent   time.Time
	pending    bool
	ev         collectedEvent
	collectors map[string]bool
}

// NewBus returns the bus of a node; local receives the events for this
// server's browsers. metrics may be nil.
func NewBus(node Node, store Repositories, local events.Publisher, metrics *obs.Metrics, log *slog.Logger) Bus {
	if metrics == nil {
		metrics = obs.NewMetrics()
	}
	return &bus{node: node, store: store, local: local, metrics: metrics, log: log, queue: make(chan envelope, 256), handlers: map[string]func(json.RawMessage){},
		collected: map[int64]*pendingCollected{}, now: time.Now}
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
	if kind == coalesceKind {
		var ev collectedEvent
		if json.Unmarshal(raw, &ev) == nil && ev.TargetID != 0 {
			b.coalesce(ev)
			return
		}
	}
	b.enqueue(kind, raw)
}

func (b *bus) enqueue(kind string, raw json.RawMessage) {
	select {
	case b.queue <- envelope{From: b.node.ID(), Kind: kind, Payload: raw}:
		b.metrics.BusQueueLength.Set(float64(len(b.queue)))
	default:
		b.metrics.BusDropped.Inc("queue_full")
		b.log.Warn("cluster bus: queue full, message dropped", "kind", kind)
	}
}

// coalesce sends a collection event at once when the target's previous one
// left more than coalesceEvery ago, and otherwise merges it into the next.
func (b *bus) coalesce(ev collectedEvent) {
	b.cmu.Lock()
	p := b.collected[ev.TargetID]
	if p == nil {
		p = &pendingCollected{}
		b.collected[ev.TargetID] = p
	}
	now := b.now()
	if !p.pending && now.Sub(p.lastSent) >= coalesceEvery {
		p.lastSent = now
		b.cmu.Unlock()
		raw, _ := json.Marshal(ev)
		b.enqueue(coalesceKind, raw)
		return
	}
	if !p.pending {
		p.pending, p.ev, p.collectors = true, ev, map[string]bool{}
	}
	p.collectors[ev.Collector] = true
	p.ev.Collector, p.ev.At = ev.Collector, ev.At
	p.ev.OK = p.ev.OK && ev.OK
	b.cmu.Unlock()
}

// flushCoalesced sends the merged collection events that are due.
func (b *bus) flushCoalesced() {
	now := b.now()
	var due []collectedEvent
	b.cmu.Lock()
	for _, p := range b.collected {
		if !p.pending || now.Sub(p.lastSent) < coalesceEvery {
			continue
		}
		ev := p.ev
		for c := range p.collectors {
			ev.Collectors = append(ev.Collectors, c)
		}
		slices.Sort(ev.Collectors)
		due = append(due, ev)
		p.pending, p.lastSent, p.collectors = false, now, nil
	}
	b.cmu.Unlock()
	for _, ev := range due {
		raw, _ := json.Marshal(ev)
		b.enqueue(coalesceKind, raw)
	}
}

// Run sends queued messages and receives the others' until ctx ends.
func (b *bus) Run(ctx context.Context) {
	go b.receive(ctx)
	flush := time.NewTicker(coalesceEvery / 4)
	defer flush.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-flush.C:
			b.flushCoalesced()
		case e := <-b.queue:
			b.metrics.BusQueueLength.Set(float64(len(b.queue)))
			raw, err := json.Marshal(e)
			if err != nil {
				continue
			}
			if len(raw) > maxMessage {
				b.metrics.BusDropped.Inc("oversized")
				b.log.Warn("cluster bus: message larger than the NOTIFY limit, not sent to the other servers",
					"kind", e.Kind, "bytes", len(raw), "limit", maxMessage)
				continue
			}
			nctx, cancel := context.WithTimeout(ctx, 5*time.Second)
			if err := b.store.Notify(nctx, channel, string(raw)); err != nil && ctx.Err() == nil {
				b.metrics.BusDropped.Inc("send_error")
				b.log.Warn("cluster bus: send failed", "kind", e.Kind, "error", err.Error())
			} else if err == nil {
				b.metrics.BusSent.Inc(e.Kind)
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
	b.metrics.BusReceived.Inc()
	b.mu.RLock()
	h := b.handlers[e.Kind]
	b.mu.RUnlock()
	if h != nil {
		h(e.Payload)
		return
	}
	b.local.Publish(e.Kind, e.Payload)
}
