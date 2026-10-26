package collector

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/locklens/locklens/internal/domain/observe"
	"github.com/locklens/locklens/internal/repository"
)

// maxLoadGroups bounds the groups of one bucket; further groups are merged
// into one "other" group so a burst of distinct applications or statements
// cannot grow storage without limit.
const maxLoadGroups = 300

// maxLoadHistory is how many finished buckets stay in memory (10 minutes),
// for the incident recorder.
const maxLoadHistory = 40

// sqlLoadSample reads the sessions that are load right now: client backends,
// parallel workers, and autovacuum workers that are not idle. Only labels
// and wait information are read; the statement is identified by query_id
// (PostgreSQL 14+, computed when compute_query_id is on or auto with
// pg_stat_statements loaded), never by its text.
const sqlLoadSample = `
SELECT a.state, a.wait_event_type, coalesce(a.wait_event, ''), coalesce(a.backend_type, ''),
       coalesce(a.datid::bigint, 0), coalesce(a.datname, ''), coalesce(a.usesysid::bigint, 0), coalesce(a.usename, ''),
       left(coalesce(a.application_name, ''), 64), coalesce(a.query_id, 0)
FROM pg_catalog.pg_stat_activity AS a
WHERE a.pid <> ALL ($1::int[])
  AND a.backend_type IN ('client backend', 'parallel worker', 'autovacuum worker')
  AND a.state IS DISTINCT FROM 'idle'`

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
	var keys []loadKey
	if err := w.obs.ReadOnly(ctx, func(ctx context.Context, tx pgx.Tx, own []int32) error {
		rows, err := tx.Query(ctx, sqlLoadSample, own)
		if err != nil {
			return err
		}
		keys, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (loadKey, error) {
			var state, waitType *string
			var k loadKey
			var dbid, userid int64
			err := r.Scan(&state, &waitType, &k.event, &k.backend, &dbid, &k.datname, &userid, &k.user, &k.app, &k.queryid)
			k.dbid, k.userid = uint32(dbid), uint32(userid)
			k.class, _ = observe.WaitClass(state, waitType)
			if k.class == observe.WaitCPU || k.class == observe.WaitIdleTx || k.class == observe.WaitUnknown {
				k.event = ""
			}
			return k, err
		})
		return err
	}); err != nil {
		return err
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
		Write: func(ctx context.Context, s *repository.Store, _ repository.IdentityCache) error {
			return s.InsertLoadBucket(ctx, id, b)
		}})
}
