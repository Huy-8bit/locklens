package collector

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/locklens/locklens/internal/domain/observe"
	"github.com/locklens/locklens/internal/repository"
	"github.com/locklens/locklens/internal/targetdb"
)

// maxLoadGroups bounds the groups of one bucket; further groups are merged
// into one "other" group so a burst of distinct applications or statements
// cannot grow storage without limit.
const maxLoadGroups = 300

// maxLoadHistory is how many finished buckets stay in memory (10 minutes),
// for the incident recorder.
const maxLoadHistory = 40

type loadKey struct {
	class, event, backend string
	dbid, userid          uint32
	datname, user, app    string
	queryid               int64
}

// loadAgg accumulates the samples of the current bucket.
type loadAgg struct {
	at     time.Time
	ticks  int
	groups map[loadKey]int
	order  []loadKey
}

func (a *loadAgg) add(k loadKey) {
	if _, ok := a.groups[k]; !ok && len(a.order) >= maxLoadGroups {
		k = loadKey{class: k.class, event: k.event, backend: k.backend, app: "(other)"}
	}
	if _, ok := a.groups[k]; !ok {
		a.order = append(a.order, k)
	}
	a.groups[k]++
}

func (a *loadAgg) bucket(interval time.Duration) observe.LoadBucket {
	b := observe.LoadBucket{At: a.at, Ticks: a.ticks, IntervalSeconds: interval.Seconds(), Groups: make([]observe.LoadGroup, 0, len(a.order))}
	for _, k := range a.order {
		b.Groups = append(b.Groups, observe.LoadGroup{WaitClass: k.class, WaitEvent: k.event, BackendType: k.backend, DBID: k.dbid,
			DatName: k.datname, UserID: k.userid, UserName: k.user, Application: k.app, QueryID: k.queryid, Samples: a.groups[k]})
	}
	return b
}

func (w *worker) collectLoad(ctx context.Context) error {
	var sessions []observe.LoadSession
	if err := w.obs.ReadOnly(ctx, func(ctx context.Context, tx pgx.Tx, own []int32) error {
		var err error
		sessions, err = targetdb.ReadLoadSample(ctx, tx, own)
		return err
	}); err != nil {
		return err
	}
	keys := make([]loadKey, len(sessions))
	for i, s := range sessions {
		k := loadKey{event: s.WaitEvent, backend: s.BackendType, dbid: s.DBID, datname: s.DatName, userid: s.UserID, user: s.UserName,
			app: s.Application, queryid: s.QueryID}
		k.class, _ = observe.WaitClass(s.State, s.WaitEventType)
		if k.class == observe.WaitCPU || k.class == observe.WaitIdleTx || k.class == observe.WaitUnknown {
			k.event = ""
		}
		keys[i] = k
	}
	now := time.Now().UTC()
	start := now.Truncate(observe.LoadBucketSize)
	if w.load != nil && !w.load.at.Equal(start) {
		w.flushLoad()
	}
	if w.load == nil {
		w.load = &loadAgg{at: start, groups: map[loadKey]int{}}
	}
	w.load.ticks++
	for _, k := range keys {
		w.load.add(k)
	}
	return nil
}

// flushLoad stores the current bucket and keeps it in the in-memory history.
func (w *worker) flushLoad() {
	if w.load == nil || w.load.ticks == 0 {
		w.load = nil
		return
	}
	b := w.load.bucket(w.m.loadEvery)
	w.load = nil
	w.state.addLoad(b)
	id := w.target.ID
	w.m.writer.Enqueue(WriteOp{TargetID: id, Collector: CollectorLoad, At: b.At,
		Write: func(ctx context.Context, s Repositories, _ repository.IdentityCache) error {
			return s.InsertLoadBucket(ctx, id, b)
		}})
}
