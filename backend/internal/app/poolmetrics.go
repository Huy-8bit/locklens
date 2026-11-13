package app

import (
	"sync"

	"github.com/locklens/locklens/internal/lib/obs"
	"github.com/locklens/locklens/internal/repository"
)

// observePool exports the monitoring store pool's statistics on every
// metrics scrape. pgxpool reports cumulative counters; their increase since
// the previous scrape is added to LockLens' counters.
func observePool(m *obs.Metrics, store *repository.Store) {
	var mu sync.Mutex
	var last struct {
		acquires, empty, canceled int64
		waitSeconds               float64
	}
	m.Registry.OnCollect(func() {
		st := store.Pool().Stat()
		m.PoolConns.Set(float64(st.AcquiredConns()), "acquired")
		m.PoolConns.Set(float64(st.IdleConns()), "idle")
		m.PoolConns.Set(float64(st.TotalConns()), "total")
		m.PoolMaxConns.Set(float64(st.MaxConns()))
		mu.Lock()
		defer mu.Unlock()
		acq, empty, canceled, wait := st.AcquireCount(), st.EmptyAcquireCount(), st.CanceledAcquireCount(), st.AcquireDuration().Seconds()
		m.PoolAcquires.Add(float64(acq - last.acquires))
		m.PoolEmptyAcquires.Add(float64(empty - last.empty))
		m.PoolCanceled.Add(float64(canceled - last.canceled))
		m.PoolAcquireWait.Add(wait - last.waitSeconds)
		last.acquires, last.empty, last.canceled, last.waitSeconds = acq, empty, canceled, wait
	})
}
