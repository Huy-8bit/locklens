package collector

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/locklens/locklens/internal/domain/observe"
	"github.com/locklens/locklens/internal/lib/hostmetrics"
	"github.com/locklens/locklens/internal/repository"
	"github.com/locklens/locklens/internal/targetdb"
)

// Collector names. They are bounded metric label values.
const (
	CollectorActivity    = "activity"
	CollectorDatabase    = "database"
	CollectorStatements  = "statements"
	CollectorReplication = "replication"
	CollectorTables      = "tables"
	CollectorHost        = "host"
	CollectorLoad        = "load"
	// CollectorTableActivity reads table counters every minute.
	CollectorTableActivity = "table_activity"
	// CollectorCatalog reads the schema for the query optimizer.
	CollectorCatalog = "catalog"
	// CollectorChanges reads settings and server facts to detect changes.
	CollectorChanges = "changes"
)

// AllCollectors lists collectors in display order.
var AllCollectors = []string{CollectorActivity, CollectorLoad, CollectorDatabase, CollectorStatements, CollectorReplication, CollectorTables,
	CollectorTableActivity, CollectorCatalog, CollectorChanges, CollectorHost}

const (
	capabilitiesMaxAge     = 10 * time.Minute
	snapshotHeartbeat      = 5 * time.Minute
	vacuumSnapshotInterval = 30 * time.Second
	maxReconnectBackoff    = time.Minute
)

type job struct {
	name string
	// base is the configured interval, burst the shorter one used while the
	// target is collected faster (0: no change), and slow the factor applied
	// when the collector exceeds its time budget. every is the interval in
	// effect.
	base    time.Duration
	burst   time.Duration
	slow    int
	over    int // consecutive runs over budget
	under   int // consecutive runs well under budget
	every   time.Duration
	timeout time.Duration
	next    time.Time
	run     func(ctx context.Context) error
	// enabled reports whether the job can run; the reason is shown in the UI.
	enabled func() (bool, string)
	status  CollectorStatus
}

type worker struct {
	m      *Manager
	target repository.Target
	state  *State

	obs  *targetdb.Collector
	caps *observe.Capabilities

	prevDB    *observe.CounterSample
	prevStmt  *statementsState
	stmtCache stmtCache
	prevHost  *hostmetrics.Scrape
	// prevTables holds the table counters of the last table activity
	// collection.
	prevTables map[string]*tableActivityState
	// dbs are the connections to the other databases in scope.
	dbs databaseSet
	// lastTableAt is the time of the last table collection of any database.
	lastTableAt time.Time

	load *loadAgg

	lastSnapshotStored time.Time
	lastVacuumStored   time.Time

	jobs            []*job
	connectFailures int
	nextConnect     time.Time
	lastConnectErr  *string
	httpClient      *http.Client
}

func newWorker(m *Manager, t repository.Target, st *State) *worker {
	w := &worker{m: m, target: t, state: st, httpClient: &http.Client{Timeout: 5 * time.Second}}
	iv := time.Duration(t.IntervalSeconds) * time.Second
	now := time.Now()
	w.jobs = []*job{
		{name: CollectorActivity, every: iv, burst: 3 * time.Second, timeout: min(iv, 15*time.Second), run: w.collectActivity},
		{name: CollectorDatabase, every: max(iv, 15*time.Second), burst: 5 * time.Second, timeout: 15 * time.Second, run: w.collectDatabase},
		{name: CollectorStatements, every: max(iv, m.stmtEvery), burst: 5 * time.Second, timeout: 30 * time.Second, run: w.collectStatements,
			enabled: func() (bool, string) {
				if w.caps == nil {
					return false, "waiting for capability detection"
				}
				if !w.caps.StatStatements {
					return false, w.caps.StatementsReason
				}
				return true, ""
			}},
		{name: CollectorReplication, every: max(iv, 30*time.Second), burst: 10 * time.Second, timeout: 15 * time.Second, run: w.collectReplication},
		{name: CollectorTables, every: 5 * time.Minute, timeout: 60 * time.Second, run: w.collectTables},
		{name: CollectorCatalog, every: 15 * time.Minute, timeout: 60 * time.Second, run: w.collectCatalog},
		{name: CollectorChanges, every: time.Minute, timeout: 15 * time.Second, run: w.collectChanges},
		{name: CollectorTableActivity, every: max(iv, time.Minute), burst: 15 * time.Second, timeout: 15 * time.Second, run: w.collectTableActivity},
		{name: CollectorLoad, every: max(m.loadEvery, time.Second), timeout: 5 * time.Second, run: w.collectLoad,
			enabled: func() (bool, string) {
				switch {
				case m.loadEvery <= 0:
					return false, "active session sampling is turned off (LOCKLENS_LOAD_SAMPLING)"
				case w.caps == nil:
					return false, "waiting for capability detection"
				case w.caps.VersionNum < 140000:
					return false, "needs PostgreSQL 14 or later (pg_stat_activity.query_id)"
				}
				return true, ""
			}},
		{name: CollectorHost, every: max(iv, 15*time.Second), burst: 5 * time.Second, timeout: 10 * time.Second, run: w.collectHost,
			enabled: func() (bool, string) {
				if w.target.NodeExporterURL == nil || *w.target.NodeExporterURL == "" {
					return false, "no node_exporter URL configured (host metrics are optional)"
				}
				return true, ""
			}},
	}
	for i, j := range w.jobs {
		// Stagger the first runs slightly so one target does not fire every
		// collector at once.
		j.next = now.Add(time.Duration(i) * 300 * time.Millisecond)
		j.base, j.slow = j.every, 1
		j.status = CollectorStatus{Name: j.name, IntervalSeconds: j.every.Seconds(), BaseIntervalSeconds: j.base.Seconds(), Enabled: true}
	}
	w.refreshEnabled()
	return w
}

// refreshEnabled re-evaluates which collectors can run and publishes every
// collector's status, so the target view lists all of them (with the reason
// a collector is unavailable) before each has had its first turn.
func (w *worker) refreshEnabled() {
	for _, j := range w.jobs {
		ok, reason := w.jobEnabled(j)
		j.status.Enabled, j.status.DisabledReason = ok, reason
		w.state.setCollector(j.status)
	}
}

func (w *worker) run(ctx context.Context) {
	defer w.close()
	for {
		j := w.jobs[0]
		for _, c := range w.jobs[1:] {
			if c.next.Before(j.next) {
				j = c
			}
		}
		if d := time.Until(j.next); d > 0 {
			timer := time.NewTimer(d)
			select {
			case <-ctx.Done():
				timer.Stop()
				return
			case <-w.state.wake:
				// A burst started or a collector was asked for: bring them
				// forward.
				timer.Stop()
				if _, ok := w.state.burst(time.Now()); ok {
					w.applyBurst()
				}
				for name := range w.state.takeRunSoon() {
					if j := w.jobByName(name); j != nil {
						j.next = time.Now()
					}
				}
				continue
			case <-timer.C:
			}
		}
		if ctx.Err() != nil {
			return
		}
		if ok, reason := w.jobEnabled(j); !ok {
			j.status.Enabled, j.status.DisabledReason = false, reason
			w.state.setCollector(j.status)
			j.next = time.Now().Add(j.every)
			continue
		}
		j.status.Enabled, j.status.DisabledReason = true, ""

		if w.obs == nil && !w.connect(ctx) {
			// Not connected: try again after the backoff, and keep every job
			// waiting until then so failures do not pile up.
			for _, x := range w.jobs {
				if x.next.Before(w.nextConnect) {
					x.next = w.nextConnect
				}
			}
			w.saveStatus(ctx)
			continue
		}
		w.runJob(ctx, j)
	}
}

func (w *worker) jobEnabled(j *job) (bool, string) {
	if j.enabled == nil {
		return true, ""
	}
	return j.enabled()
}

func (w *worker) runJob(ctx context.Context, j *job) {
	select {
	case w.m.sem <- struct{}{}:
	case <-ctx.Done():
		return
	}
	w.m.slotsBusy.Add(1)
	w.m.metrics.CollectSlotsBusy.Set(float64(w.m.slotsBusy.Load()))
	start := time.Now()
	jctx, cancel := context.WithTimeout(ctx, j.timeout)
	err := j.run(jctx)
	cancel()
	<-w.m.sem
	w.m.slotsBusy.Add(-1)
	w.m.metrics.CollectSlotsBusy.Set(float64(w.m.slotsBusy.Load()))
	if ctx.Err() != nil {
		return
	}
	dur := time.Since(start)
	now := start.UTC()
	ms := dur.Milliseconds()

	// A late or failed collector leaves a hole in the history: record it.
	// Load sampling records its own coverage (ticks per bucket) instead.
	if j.status.LastSuccess != nil && j.name != CollectorLoad {
		late := start.Sub(*j.status.LastSuccess)
		if late > 2*j.every+j.timeout {
			reason := "collector did not run"
			if j.status.LastError != nil {
				reason = *j.status.LastError
			}
			from := *j.status.LastSuccess
			w.m.writer.Enqueue(WriteOp{TargetID: w.target.ID, Collector: j.name, At: now,
				Write: func(ctx context.Context, s Store, _ repository.IdentityCache) error {
					return s.InsertGap(ctx, w.target.ID, j.name, from, now, reason)
				}})
		}
	}

	wasFailing := j.status.LastError != nil
	j.status.LastAttempt = &now
	j.status.LastDurationMS = &ms
	w.m.metrics.CollectDuration.Observe(dur.Seconds(), j.name)
	if err != nil {
		msg := sanitizeError(err)
		j.status.LastError = &msg
		j.status.ConsecutiveFailures++
		w.m.metrics.CollectTotal.Inc(j.name, "error")
		w.m.log.Warn("collector failed", "target_id", w.target.ID, "collector", j.name, "error", msg)
		if isConnectionError(err) {
			w.disconnect(err)
		}
	} else {
		j.status.LastError = nil
		j.status.LastSuccess = &now
		j.status.ConsecutiveFailures = 0
		w.m.metrics.CollectTotal.Inc(j.name, "ok")
	}
	w.state.setCollector(j.status)

	w.pace(j, dur)
	next := start.Add(j.every)
	if next.Before(time.Now()) {
		next = time.Now().Add(j.every / 2)
	}
	j.next = next

	// Load sampling runs every few seconds: its status is stored and
	// announced only when it starts or stops failing, so it neither writes
	// the status row nor refreshes every open page at that rate.
	if j.name == CollectorLoad && wasFailing == (err != nil) {
		return
	}

	if j.name == CollectorActivity && err == nil {
		w.m.metrics.CollectFreshness.Set(float64(now.Unix()), strconv.FormatInt(w.target.ID, 10))
		if w.m.analyzer != nil {
			actx, cancel := context.WithTimeout(ctx, 10*time.Second)
			w.m.analyzer.Analyze(actx, w.target, w.state.Snapshot())
			cancel()
		}
	}
	w.saveStatus(ctx)
	w.m.events.Publish("target.collected", map[string]any{"target_id": w.target.ID, "collector": j.name, "at": now, "ok": err == nil})
}

// maxSlow bounds how much a collector over its budget is slowed down.
const maxSlow = 8

// pace sets a job's next interval: faster during a burst, slower while its
// runs take more than the time budget (a share of the interval), and back
// to normal once they are well under it again. It protects the target from
// LockLens itself: a collector that is expensive on this server runs less
// often, and the status says so.
func (w *worker) pace(j *job, took time.Duration) {
	limit := time.Duration(float64(j.every) * w.m.budget)
	switch {
	case took > limit:
		j.over, j.under = j.over+1, 0
	case took < limit/2:
		j.under, j.over = j.under+1, 0
	default:
		j.over, j.under = 0, 0
	}
	if j.over >= 3 && j.slow < maxSlow {
		j.slow *= 2
		j.over = 0
		j.status.ThrottleReason = fmt.Sprintf("runs %d× less often: collections took %d ms, more than %.0f%% of the interval",
			j.slow, took.Milliseconds(), w.m.budget*100)
		w.m.log.Warn("collector slowed down", "target_id", w.target.ID, "collector", j.name, "factor", j.slow, "took_ms", took.Milliseconds())
	}
	if j.under >= 20 && j.slow > 1 {
		j.slow /= 2
		j.under = 0
		if j.slow == 1 {
			j.status.ThrottleReason = ""
		}
	}
	j.every = w.interval(j, time.Now())
	j.status.IntervalSeconds = j.every.Seconds()
	_, burst := w.state.burst(time.Now())
	j.status.Burst = burst && j.burst > 0
	w.state.setCollector(j.status)
}

// interval is a job's interval in effect at now.
func (w *worker) interval(j *job, now time.Time) time.Duration {
	e := j.base
	if _, ok := w.state.burst(now); ok && j.burst > 0 && j.burst < e {
		e = j.burst
	}
	return e * time.Duration(max(j.slow, 1))
}

// applyBurst shortens the intervals of the collectors that run faster
// during a burst and runs them soon.
func (w *worker) applyBurst() {
	now := time.Now()
	for _, j := range w.jobs {
		if j.burst <= 0 {
			continue
		}
		j.every = w.interval(j, now)
		if j.next.After(now.Add(j.every)) {
			j.next = now.Add(j.every)
		}
		j.status.IntervalSeconds, j.status.Burst = j.every.Seconds(), true
		w.state.setCollector(j.status)
	}
}

// saveStatus writes the target status row.
func (w *worker) saveStatus(ctx context.Context) {
	v := w.state.Snapshot()
	st := repository.TargetStatus{TargetID: w.target.ID}
	st.State = ComputeState(w.target.Enabled, v, time.Duration(w.target.IntervalSeconds)*time.Second, time.Now())
	if w.caps != nil {
		ver, num, sb := w.caps.ServerVersion, w.caps.VersionNum, w.caps.IsStandby
		st.ServerVersion, st.ServerVersionNum, st.IsStandby = &ver, &num, &sb
		st.Capabilities, _ = json.Marshal(w.caps)
	}
	st.Collectors, _ = json.Marshal(v.Collectors)
	if a, ok := v.Collectors[CollectorActivity]; ok {
		st.LastAttemptAt, st.LastSuccessAt = a.LastAttempt, a.LastSuccess
		if a.LastError != nil {
			st.LastError, st.LastErrorAt = a.LastError, a.LastAttempt
		}
	}
	if !v.Connected && v.ConnectError != nil {
		st.LastError, st.LastErrorAt = v.ConnectError, v.ConnectAt
		st.LastAttemptAt = v.ConnectAt
	}
	sctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if err := w.m.store.SaveTargetStatus(sctx, st); err != nil {
		w.m.log.Warn("save target status failed", "target_id", w.target.ID, "error", err.Error())
	}
}

func (w *worker) jobByName(name string) *job {
	for _, j := range w.jobs {
		if j.name == name {
			return j
		}
	}
	return nil
}
