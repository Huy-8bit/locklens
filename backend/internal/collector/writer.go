package collector

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/locklens/locklens/internal/obs"
	"github.com/locklens/locklens/internal/storage"
)

// WriteOp is one unit of collected data destined for the monitoring store.
type WriteOp struct {
	TargetID  int64
	Collector string
	At        time.Time
	Write     func(ctx context.Context, s *storage.Store, cache storage.IdentityCache) error
}

// Writer decouples collection from the monitoring store with a bounded
// queue. When the store is slow or down, the queue fills; once full, the
// oldest operation is dropped, counted, and remembered as a collection gap
// that is written when the store accepts writes again. Memory use is
// therefore bounded by the queue capacity, and lost data is never hidden.
type Writer struct {
	store   *storage.Store
	metrics *obs.Metrics
	log     *slog.Logger

	mu      sync.Mutex
	queue   []WriteOp
	cap     int
	notify  chan struct{}
	dropped []WriteOp // metadata of dropped ops, bounded by cap
	cache   storage.IdentityCache
	// writeGap records a dropped operation; replaceable in tests.
	writeGap func(ctx context.Context, op WriteOp) error
}

// NewWriter returns a writer with the given queue capacity.
func NewWriter(store *storage.Store, capacity int, metrics *obs.Metrics, log *slog.Logger) *Writer {
	if capacity < 1 {
		capacity = 1
	}
	metrics.WriteQueueCap.Set(float64(capacity))
	w := &Writer{store: store, metrics: metrics, log: log, cap: capacity, notify: make(chan struct{}, 1), cache: storage.IdentityCache{}}
	w.writeGap = func(ctx context.Context, op WriteOp) error {
		return store.InsertGap(ctx, op.TargetID, op.Collector, op.At, op.At, "data dropped: monitoring store unavailable or write queue full")
	}
	return w
}

// Enqueue adds an operation without blocking.
func (w *Writer) Enqueue(op WriteOp) {
	w.mu.Lock()
	if len(w.queue) >= w.cap {
		old := w.queue[0]
		w.queue = w.queue[1:]
		w.metrics.StoreDropped.Inc()
		if len(w.dropped) < w.cap {
			w.dropped = append(w.dropped, WriteOp{TargetID: old.TargetID, Collector: old.Collector, At: old.At})
		}
	}
	w.queue = append(w.queue, op)
	w.metrics.WriteQueueLength.Set(float64(len(w.queue)))
	w.mu.Unlock()
	select {
	case w.notify <- struct{}{}:
	default:
	}
}

// Len returns the number of queued operations.
func (w *Writer) Len() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return len(w.queue)
}

func (w *Writer) next() (WriteOp, bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if len(w.queue) == 0 {
		return WriteOp{}, false
	}
	op := w.queue[0]
	w.queue = w.queue[1:]
	w.metrics.WriteQueueLength.Set(float64(len(w.queue)))
	return op, true
}

// Run writes queued operations until ctx is cancelled, then drains for up to
// five seconds. A failing write is retried with backoff; while it is retried
// new operations keep queueing (and the oldest are dropped when full).
func (w *Writer) Run(ctx context.Context) {
	for {
		op, ok := w.next()
		if !ok {
			select {
			case <-ctx.Done():
				w.drain()
				return
			case <-w.notify:
				continue
			}
		}
		w.writeWithRetry(ctx, op)
		if ctx.Err() != nil {
			w.drain()
			return
		}
	}
}

func (w *Writer) writeWithRetry(ctx context.Context, op WriteOp) {
	backoff := 200 * time.Millisecond
	for attempt := 1; ; attempt++ {
		wctx, cancel := context.WithTimeout(ctx, 15*time.Second)
		err := op.Write(wctx, w.store, w.cache)
		cancel()
		if err == nil {
			w.flushDropped(ctx)
			return
		}
		w.metrics.StoreWriteErrors.Inc()
		if attempt == 1 || attempt%10 == 0 {
			w.log.Warn("monitoring store write failed; retrying", "collector", op.Collector, "target_id", op.TargetID,
				"attempt", attempt, "error", err.Error())
		}
		// Give up on this operation after a few attempts if newer data is
		// waiting, so a permanently failing write cannot block the queue.
		if attempt >= 5 && w.Len() > 0 {
			w.mu.Lock()
			if len(w.dropped) < w.cap {
				w.dropped = append(w.dropped, WriteOp{TargetID: op.TargetID, Collector: op.Collector, At: op.At})
			}
			w.mu.Unlock()
			w.metrics.StoreDropped.Inc()
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		backoff = min(backoff*2, 5*time.Second)
	}
}

// flushDropped records dropped operations as collection gaps.
func (w *Writer) flushDropped(ctx context.Context) {
	w.mu.Lock()
	dropped := w.dropped
	w.dropped = nil
	w.mu.Unlock()
	for i, d := range dropped {
		gctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		err := w.writeGap(gctx, d)
		cancel()
		if err != nil {
			w.mu.Lock()
			w.dropped = append(dropped[i:], w.dropped...)
			if len(w.dropped) > w.cap {
				w.dropped = w.dropped[:w.cap]
			}
			w.mu.Unlock()
			return
		}
	}
}

func (w *Writer) drain() {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for ctx.Err() == nil {
		op, ok := w.next()
		if !ok {
			return
		}
		if err := op.Write(ctx, w.store, w.cache); err != nil {
			w.metrics.StoreWriteErrors.Inc()
			return
		}
	}
}
