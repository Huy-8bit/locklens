package collector

import (
	"context"
	"encoding/json"
	"hash/fnv"
	"sync"
	"time"

	"github.com/locklens/locklens/internal/repository"
)

// statusEvery is how often the collector statuses of this server's targets
// are written to the store (sooner when a target's state changes).
const statusEvery = 5 * time.Second

// statusWriter gathers the latest collector status of every target this
// server collects and writes the changed ones in one batch per tick, instead
// of one store write after every collector run (tens per second with a few
// hundred targets). A status identical to the one last written is skipped;
// a change of state (healthy, degraded, unreachable, …) is written at once.
type statusWriter struct {
	mu      sync.Mutex
	pending map[int64]repository.TargetStatus
	written map[int64]uint64 // hash of the last status written
	state   map[int64]string // state last written
	urgent  chan struct{}
}

func newStatusWriter() *statusWriter {
	return &statusWriter{pending: map[int64]repository.TargetStatus{}, written: map[int64]uint64{},
		state: map[int64]string{}, urgent: make(chan struct{}, 1)}
}

// put records a target's latest status; it replaces one not yet written.
func (s *statusWriter) put(st repository.TargetStatus) {
	s.mu.Lock()
	s.pending[st.TargetID] = st
	last, known := s.state[st.TargetID]
	s.mu.Unlock()
	if !known || last != st.State {
		select {
		case s.urgent <- struct{}{}:
		default:
		}
	}
}

// forget drops what is known of a target this server no longer collects.
func (s *statusWriter) forget(id int64) {
	s.mu.Lock()
	delete(s.pending, id)
	delete(s.written, id)
	delete(s.state, id)
	s.mu.Unlock()
}

// take returns the pending statuses that differ from the ones last written.
func (s *statusWriter) take() ([]repository.TargetStatus, []uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []repository.TargetStatus
	var hashes []uint64
	for id, st := range s.pending {
		delete(s.pending, id)
		h := statusHash(st)
		if w, ok := s.written[id]; ok && w == h {
			continue
		}
		out = append(out, st)
		hashes = append(hashes, h)
	}
	return out, hashes
}

// done records the outcome of a write: written statuses are remembered, failed
// ones are kept for the next tick unless a newer status arrived meanwhile.
func (s *statusWriter) done(sts []repository.TargetStatus, hashes []uint64, failed map[int64]bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, st := range sts {
		if failed[st.TargetID] {
			if _, newer := s.pending[st.TargetID]; !newer {
				s.pending[st.TargetID] = st
			}
			continue
		}
		s.written[st.TargetID] = hashes[i]
		s.state[st.TargetID] = st.State
	}
}

func statusHash(st repository.TargetStatus) uint64 {
	raw, _ := json.Marshal(st)
	h := fnv.New64a()
	h.Write(raw)
	return h.Sum64()
}

// runStatus writes the gathered statuses every statusEvery, and at once when
// a state changed, until ctx ends (Run writes the last ones after stopping
// the workers).
func (m *manager) runStatus(ctx context.Context) {
	t := time.NewTicker(statusEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		case <-m.status.urgent:
		}
		m.flushStatus(ctx)
	}
}

// flushStatus writes the changed statuses in one batch.
func (m *manager) flushStatus(ctx context.Context) {
	sts, hashes := m.status.take()
	if len(sts) == 0 {
		return
	}
	wctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	// Statuses of targets this server may no longer hold are not written
	// (the new holder writes them); the others are fenced.
	fences := make([]repository.Fence, 0, len(sts))
	held := sts[:0:0]
	heldHashes := hashes[:0:0]
	var lost []repository.TargetStatus
	var lostHashes []uint64
	for i, st := range sts {
		f, ok := m.cluster.Fence(st.TargetID)
		if !ok {
			lost, lostHashes = append(lost, st), append(lostHashes, hashes[i])
			continue
		}
		fences = append(fences, f)
		held, heldHashes = append(held, st), append(heldHashes, hashes[i])
	}
	from := time.Now()
	failedIDs, fencedIDs, err := m.store.SaveTargetStatuses(wctx, held, fences)
	m.metrics.StatusWrite.Observe(time.Since(from).Seconds())
	failed := make(map[int64]bool, len(failedIDs))
	for _, id := range failedIDs {
		failed[id] = true
	}
	for _, id := range fencedIDs {
		m.leaseLost("status", id)
	}
	// A target this server no longer has: dropped (the new holder writes
	// its status). One it still has but may have lost (self-fenced): kept for
	// the next tick.
	retry := map[int64]bool{}
	for _, st := range lost {
		if m.cluster.Owns(st.TargetID) {
			retry[st.TargetID] = true
		}
	}
	m.status.done(lost, lostHashes, retry)
	sts, hashes = held, heldHashes
	if err != nil {
		m.log.Warn("saving collector statuses failed", "targets", len(failedIDs), "of", len(sts), "error", err.Error())
	}
	m.status.done(sts, hashes, failed)
}
