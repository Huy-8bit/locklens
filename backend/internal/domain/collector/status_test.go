package collector

import (
	"encoding/json"
	"testing"

	"github.com/locklens/locklens/internal/repository"
)

func status(id int64, state, collectors string) repository.TargetStatus {
	return repository.TargetStatus{TargetID: id, State: state, Collectors: json.RawMessage(collectors)}
}

func urgent(s *statusWriter) bool {
	select {
	case <-s.urgent:
		return true
	default:
		return false
	}
}

func TestStatusWriterBatchesAndSkipsUnchanged(t *testing.T) {
	s := newStatusWriter()

	// The first status of a target is written at once (its state is new).
	s.put(status(1, "healthy", `{"a":1}`))
	if !urgent(s) {
		t.Fatal("a target's first status is urgent")
	}
	// Later statuses only replace the pending one: one write per tick.
	s.put(status(1, "healthy", `{"a":2}`))
	s.put(status(2, "healthy", `{}`))
	sts, hashes := s.take()
	if len(sts) != 2 {
		t.Fatalf("pending: %d, want 2 (latest per target)", len(sts))
	}
	for _, st := range sts {
		if st.TargetID == 1 && string(st.Collectors) != `{"a":2}` {
			t.Fatalf("target 1 kept an older status: %s", st.Collectors)
		}
	}
	s.done(sts, hashes, nil)
	_ = urgent(s)

	// Same status again: nothing to write. Same state, other content: written
	// at the next tick, not at once.
	s.put(status(2, "healthy", `{}`))
	s.put(status(1, "healthy", `{"a":3}`))
	if urgent(s) {
		t.Fatal("an unchanged state is not urgent")
	}
	sts, hashes = s.take()
	if len(sts) != 1 || sts[0].TargetID != 1 {
		t.Fatalf("after a tick: %+v", sts)
	}
	s.done(sts, hashes, nil)

	// A change of state is urgent.
	s.put(status(1, "unreachable", `{"a":3}`))
	if !urgent(s) {
		t.Fatal("a state change is urgent")
	}

	// A failed write is retried at the next tick, unless a newer status arrived.
	sts, hashes = s.take()
	s.done(sts, hashes, map[int64]bool{1: true})
	if again, _ := s.take(); len(again) != 1 || again[0].State != "unreachable" {
		t.Fatalf("failed status not retried: %+v", again)
	}
	s.put(status(3, "healthy", `{}`))
	sts, hashes = s.take()
	s.put(status(3, "degraded", `{}`)) // newer, arrives during the write
	s.done(sts, hashes, map[int64]bool{3: true})
	if again, _ := s.take(); len(again) != 1 || again[0].State != "degraded" {
		t.Fatalf("a failed write must not replace a newer status: %+v", again)
	}
}
