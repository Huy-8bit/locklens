package collector

import (
	"context"
	"testing"
	"time"

	"github.com/locklens/locklens/internal/repository"
)

type liveStore struct {
	repository.ClusterRepository
	rows  map[int64]repository.LiveState
	calls int
}

func (l *liveStore) LiveStates(_ context.Context, ids []int64) ([]repository.LiveState, error) {
	l.calls++
	var out []repository.LiveState
	for _, id := range ids {
		if r, ok := l.rows[id]; ok {
			out = append(out, r)
		}
	}
	return out, nil
}

func TestRemoteViewsGetManyReadsOnceAndCaches(t *testing.T) {
	now := time.Now()
	ls := &liveStore{rows: map[int64]repository.LiveState{
		1: {TargetID: 1, At: now, View: []byte(`{"Connected":true}`)},
		2: {TargetID: 2, At: now.Add(-2 * time.Minute), View: []byte(`{"Connected":true}`)}, // too old
		3: {TargetID: 3, At: now, View: []byte(`not json`)},
	}}
	store := Repositories{ClusterRepository: ls}
	var r remoteViews

	got := r.getMany(context.Background(), store, []int64{1, 2, 3, 4})
	if ls.calls != 1 {
		t.Fatalf("store reads: %d, want 1", ls.calls)
	}
	if v, ok := got[1]; !ok || !v.Connected {
		t.Fatalf("target 1: %+v %v", v, ok)
	}
	for _, id := range []int64{2, 3, 4} {
		if _, ok := got[id]; ok {
			t.Fatalf("target %d: an old, invalid, or missing view must be absent", id)
		}
	}
	// Within the cache window nothing is read again, absent views included.
	again := r.getMany(context.Background(), store, []int64{1, 2, 3, 4})
	if ls.calls != 1 || len(again) != 1 {
		t.Fatalf("second read: calls %d, views %d", ls.calls, len(again))
	}
}
