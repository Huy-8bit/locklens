package collector

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/locklens/locklens/internal/domain/observe"
	"github.com/locklens/locklens/internal/lib/hostmetrics"
	"github.com/locklens/locklens/internal/lib/postgres"
	"github.com/locklens/locklens/internal/repository"
)

// Collector names. They are bounded metric label values.
const (
	CollectorActivity    = "activity"
	CollectorDatabase    = "database"
	CollectorStatements  = "statements"
	CollectorReplication = "replication"
	CollectorTables      = "tables"
	CollectorHost        = "host"
)

// AllCollectors lists collectors in display order.
var AllCollectors = []string{CollectorActivity, CollectorDatabase, CollectorStatements, CollectorReplication, CollectorTables, CollectorHost}

const (
	capabilitiesMaxAge     = 10 * time.Minute
	snapshotHeartbeat      = 5 * time.Minute
	vacuumSnapshotInterval = 30 * time.Second
	maxReconnectBackoff    = time.Minute
)

type job struct {
	name    string
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

	obs  *postgres.Collector
	caps *observe.Capabilities

	prevDB   *CounterSample
	prevStmt *statementsState
	prevHost *hostmetrics.Scrape

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
		{name: CollectorActivity, every: iv, timeout: min(iv, 15*time.Second), run: w.collectActivity},
		{name: CollectorDatabase, every: max(iv, 15*time.Second), timeout: 15 * time.Second, run: w.collectDatabase},
		{name: CollectorStatements, every: max(iv, m.stmtEvery), timeout: 30 * time.Second, run: w.collectStatements,
			enabled: func() (bool, string) {
				if w.caps == nil {
					return false, "waiting for capability detection"
				}
				if !w.caps.StatStatements {
					return false, w.caps.StatementsReason
				}
				return true, ""
			}},
		{name: CollectorReplication, every: max(iv, 30*time.Second), timeout: 15 * time.Second, run: w.collectReplication},
		{name: CollectorTables, every: 5 * time.Minute, timeout: 60 * time.Second, run: w.collectTables},
		{name: CollectorHost, every: max(iv, 15*time.Second), timeout: 10 * time.Second, run: w.collectHost,
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
		j.status = CollectorStatus{Name: j.name, IntervalSeconds: j.every.Seconds(), Enabled: true}
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
	if j.status.LastSuccess != nil {
		late := start.Sub(*j.status.LastSuccess)
		if late > 2*j.every+j.timeout {
			reason := "collector did not run"
			if j.status.LastError != nil {
				reason = *j.status.LastError
			}
			from := *j.status.LastSuccess
			w.m.writer.Enqueue(WriteOp{TargetID: w.target.ID, Collector: j.name, At: now,
				Write: func(ctx context.Context, s *repository.Store, _ repository.IdentityCache) error {
					return s.InsertGap(ctx, w.target.ID, j.name, from, now, reason)
				}})
		}
	}

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

	next := start.Add(j.every)
	if next.Before(time.Now()) {
		next = time.Now().Add(j.every / 2)
	}
	j.next = next

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
