package collector

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/locklens/locklens/internal/lib/obs"
	"github.com/locklens/locklens/internal/repository"
)

func testWriter(capacity int) *Writer {
	w := NewWriter(Repositories{}, capacity, obs.NewMetrics(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	return w
}

func TestWriterDropsOldestWhenFull(t *testing.T) {
	w := testWriter(3)
	var gaps []string
	var mu sync.Mutex
	w.writeGap = func(_ context.Context, op WriteOp) error {
		mu.Lock()
		gaps = append(gaps, op.Collector)
		mu.Unlock()
		return nil
	}
	for _, name := range []string{"a", "b", "c", "d", "e"} {
		w.Enqueue(WriteOp{Collector: name, Write: func(context.Context, Repositories, repository.IdentityCache) error { return nil }})
	}
	if w.Len() != 3 {
		t.Fatalf("queue length = %d, want 3 (bounded)", w.Len())
	}
	op, _ := w.next()
	if op.Collector != "c" {
		t.Fatalf("oldest remaining = %s, want c", op.Collector)
	}
	w.flushDropped(context.Background())
	if len(gaps) != 2 || gaps[0] != "a" || gaps[1] != "b" {
		t.Fatalf("dropped ops must become gaps, got %v", gaps)
	}
}

func TestWriterRetriesThenSucceeds(t *testing.T) {
	w := testWriter(10)
	w.writeGap = func(context.Context, WriteOp) error { return nil }
	var calls atomic.Int32
	done := make(chan struct{})
	w.Enqueue(WriteOp{Collector: "x", Write: func(context.Context, Repositories, repository.IdentityCache) error {
		if calls.Add(1) < 3 {
			return errors.New("store down")
		}
		close(done)
		return nil
	}})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go w.Run(ctx)
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("write was not retried to success")
	}
	if calls.Load() != 3 {
		t.Errorf("attempts = %d", calls.Load())
	}
}

func TestWriterGivesUpOnPoisonOpWhenNewerDataWaits(t *testing.T) {
	w := testWriter(10)
	w.writeGap = func(context.Context, WriteOp) error { return nil }
	ok := make(chan struct{})
	w.Enqueue(WriteOp{Collector: "poison", Write: func(context.Context, Repositories, repository.IdentityCache) error {
		return errors.New("always fails")
	}})
	w.Enqueue(WriteOp{Collector: "good", Write: func(context.Context, Repositories, repository.IdentityCache) error { close(ok); return nil }})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go w.Run(ctx)
	select {
	case <-ok:
	case <-time.After(15 * time.Second):
		t.Fatal("a failing operation blocked the queue")
	}
}
