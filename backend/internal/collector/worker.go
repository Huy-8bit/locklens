package collector

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/locklens/locklens/internal/config"
	"github.com/locklens/locklens/internal/hostmetrics"
	"github.com/locklens/locklens/internal/model"
	"github.com/locklens/locklens/internal/observe"
	"github.com/locklens/locklens/internal/postgres"
	"github.com/locklens/locklens/internal/report"
	"github.com/locklens/locklens/internal/storage"
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
	target storage.Target
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

func newWorker(m *Manager, t storage.Target, st *State) *worker {
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

func (w *worker) connect(ctx context.Context) bool {
	if time.Now().Before(w.nextConnect) {
		return false
	}
	now := time.Now().UTC()
	err := func() error {
		plain, err := w.m.box.Open(w.target.DSNCiphertext, []byte(DSNAdditionalData))
		if err != nil {
			return err
		}
		cfg, red, err := config.PoolConfig(string(plain), config.Options{ConnectTimeout: 5 * time.Second, QueryTimeout: 30 * time.Second})
		if err != nil {
			return err
		}
		cfg.MaxConns = int32(w.target.MaxConnections)
		cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		obs, err := postgres.Connect(cctx, cfg, red, model.Tool{Name: "locklens", Version: w.m.version})
		if err != nil {
			return err
		}
		var caps observe.Capabilities
		if err := obs.ReadOnly(cctx, func(ctx context.Context, tx pgx.Tx, _ []int32) error {
			var err error
			caps, err = detectCapabilities(ctx, tx)
			return err
		}); err != nil {
			obs.Close()
			return err
		}
		if caps.VersionNum < postgres.MinServerVersionNum {
			obs.Close()
			return &postgres.UnsupportedVersionError{Version: caps.ServerVersion}
		}
		w.obs, w.caps = obs, &caps
		return nil
	}()
	w.state.mu.Lock()
	w.state.connectAt = &now
	if err != nil {
		msg := sanitizeError(err)
		w.lastConnectErr = &msg
		w.state.connected = false
		w.state.connectError = &msg
		w.state.mu.Unlock()
		w.connectFailures++
		backoff := time.Duration(w.target.IntervalSeconds) * time.Second
		for i := 1; i < w.connectFailures && backoff < maxReconnectBackoff; i++ {
			backoff *= 2
		}
		w.nextConnect = time.Now().Add(min(backoff, maxReconnectBackoff))
		w.m.log.Warn("target connection failed", "target_id", w.target.ID, "target", w.target.Name, "error", msg)
		w.m.events.Publish("target.status", map[string]any{"target_id": w.target.ID})
		return false
	}
	w.state.connected = true
	w.state.connectError = nil
	w.state.caps = w.caps
	w.state.mu.Unlock()
	w.lastConnectErr = nil
	w.connectFailures = 0
	w.refreshEnabled()
	return true
}

func (w *worker) close() {
	if w.obs != nil {
		w.obs.Close()
		w.obs = nil
	}
}

func (w *worker) disconnect(err error) {
	w.close()
	msg := sanitizeError(err)
	w.lastConnectErr = &msg
	w.state.mu.Lock()
	w.state.connected = false
	w.state.connectError = &msg
	w.state.mu.Unlock()
	w.connectFailures = 1
	w.nextConnect = time.Now().Add(time.Duration(w.target.IntervalSeconds) * time.Second)
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
				Write: func(ctx context.Context, s *storage.Store, _ storage.IdentityCache) error {
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
	st := storage.TargetStatus{TargetID: w.target.ID}
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

// Target monitoring states.
const (
	StatePending     = "pending"
	StateHealthy     = "healthy"
	StateDegraded    = "degraded"
	StateUnreachable = "unreachable"
	StateDisabled    = "disabled"
)

// ComputeState derives the documented target state:
//   - disabled: collection switched off;
//   - pending: no connection attempt yet;
//   - unreachable: the last connection attempt failed, or the activity
//     collector has not succeeded within three intervals;
//   - degraded: connected, but a collector's latest run failed, or the data is
//     partial (role without pg_read_all_stats, track_activities off);
//   - healthy: everything else.
//
// It describes LockLens' ability to monitor the target, not the health of
// the database itself.
func ComputeState(enabled bool, v View, interval time.Duration, now time.Time) string {
	if !enabled {
		return StateDisabled
	}
	if v.ConnectAt == nil {
		return StatePending
	}
	if !v.Connected {
		return StateUnreachable
	}
	a, ok := v.Collectors[CollectorActivity]
	if !ok || a.LastAttempt == nil {
		return StatePending
	}
	if a.LastSuccess == nil || now.Sub(*a.LastSuccess) > 3*interval+15*time.Second {
		return StateUnreachable
	}
	for _, c := range v.Collectors {
		if c.Enabled && c.LastError != nil {
			return StateDegraded
		}
	}
	if v.Capabilities != nil && (!v.Capabilities.ReadAllStats || !v.Capabilities.TrackActivities) {
		return StateDegraded
	}
	return StateHealthy
}

func (w *worker) refreshCapabilities(ctx context.Context) error {
	if w.caps != nil && time.Since(w.caps.DetectedAt) < capabilitiesMaxAge {
		return nil
	}
	return w.obs.ReadOnly(ctx, func(ctx context.Context, tx pgx.Tx, _ []int32) error {
		caps, err := detectCapabilities(ctx, tx)
		if err != nil {
			return err
		}
		w.caps = &caps
		w.state.mu.Lock()
		w.state.caps = &caps
		w.state.mu.Unlock()
		return nil
	})
}

func (w *worker) collectActivity(ctx context.Context) error {
	longTx := w.m.settings.LongTransactionThreshold()
	snap, err := w.obs.Collect(ctx, postgres.Options{IncludeQuery: w.target.QueryTextEnabled, QueryMaxLength: 2000, MinTxAge: longTx})
	if err != nil {
		return err
	}
	var counts observe.ActivityCounts
	var vacuum []observe.VacuumProgress
	if err := w.obs.ReadOnly(ctx, func(ctx context.Context, tx pgx.Tx, own []int32) error {
		var err error
		if counts, err = readActivityCounts(ctx, tx, own); err != nil {
			return err
		}
		vacuum, err = readVacuumProgress(ctx, tx)
		return err
	}); err != nil {
		return err
	}
	rep := report.Build(snap, longTx)
	// All server-side series use LockLens' clock so that collectors of one
	// target line up; ages inside the report still use the server clock.
	at := snap.CollectedAt
	act := &observe.Activity{At: at, Snapshot: snap, Counts: counts, Vacuum: vacuum}
	points := activityPoints(counts, rep)

	w.state.mu.Lock()
	w.state.activity, w.state.report = act, rep
	w.state.mu.Unlock()
	w.state.addPoints(at, points)

	id := w.target.ID
	w.m.writer.Enqueue(WriteOp{TargetID: id, Collector: CollectorActivity, At: at,
		Write: func(ctx context.Context, s *storage.Store, _ storage.IdentityCache) error {
			return s.InsertPoints(ctx, id, at, points)
		}})
	interesting := rep.Analysis.Summary.BlockedSessions > 0 || rep.Analysis.Summary.LongTransactions > 0
	if interesting || at.Sub(w.lastSnapshotStored) >= snapshotHeartbeat {
		w.lastSnapshotStored = at
		raw, err := json.Marshal(snap)
		if err != nil {
			return err
		}
		sum := rep.Analysis.Summary
		w.m.writer.Enqueue(WriteOp{TargetID: id, Collector: CollectorActivity, At: at,
			Write: func(ctx context.Context, s *storage.Store, _ storage.IdentityCache) error {
				return s.InsertActivitySnapshot(ctx, id, at, sum.BlockedSessions, sum.RootBlockers, sum.LongTransactions, raw)
			}})
	}
	if len(vacuum) > 0 && at.Sub(w.lastVacuumStored) >= vacuumSnapshotInterval {
		w.lastVacuumStored = at
		raw, _ := json.Marshal(vacuum)
		w.m.writer.Enqueue(WriteOp{TargetID: id, Collector: CollectorActivity, At: at,
			Write: func(ctx context.Context, s *storage.Store, _ storage.IdentityCache) error {
				return s.InsertStateSnapshot(ctx, id, "vacuum_progress", at, raw)
			}})
	}
	return nil
}

// activityPoints derives the session, connection, blocking, and transaction
// series from one activity collection.
func activityPoints(c observe.ActivityCounts, rep *report.Report) []observe.Point {
	f := func(v int) *float64 { return observe.F(float64(v)) }
	pts := []observe.Point{
		{Metric: "sessions.active", Value: f(c.Active)},
		{Metric: "sessions.idle", Value: f(c.Idle)},
		{Metric: "sessions.idle_in_transaction", Value: f(c.IdleInTx + c.IdleInTxAborted)},
		{Metric: "sessions.unknown_state", Value: f(c.UnknownState)},
		{Metric: "sessions.waiting_lock", Value: f(c.WaitingLock)},
		{Metric: "sessions.client_total", Value: f(c.ClientTotal)},
		{Metric: "connections.used", Value: f(c.UsedConnections)},
		{Metric: "connections.max", Value: f(c.MaxConnections)},
		{Metric: "connections.reserved", Value: f(c.ReservedConnections)},
	}
	if avail := c.MaxConnections - c.ReservedConnections; avail > 0 {
		pts = append(pts, observe.Point{Metric: "connections.usage_ratio", Value: observe.F(float64(c.UsedConnections) / float64(avail))})
	}
	sum := rep.Analysis.Summary
	pts = append(pts,
		observe.Point{Metric: "blocking.blocked_sessions", Value: f(sum.BlockedSessions)},
		observe.Point{Metric: "blocking.root_blockers", Value: f(sum.RootBlockers)},
		observe.Point{Metric: "transactions.long", Value: f(sum.LongTransactions)},
	)
	var maxWait, oldest, oldestIdle *float64
	open := 0
	byPID := map[int]model.Session{}
	for _, s := range rep.Snapshot.Sessions {
		byPID[s.PID] = s
	}
	for _, a := range rep.Analysis.Sessions {
		if a.LockWaitSeconds != nil && (maxWait == nil || *a.LockWaitSeconds > *maxWait) {
			maxWait = a.LockWaitSeconds
		}
		s := byPID[a.PID]
		if s.BackendType == nil || *s.BackendType != "client backend" || a.TransactionAgeSeconds == nil {
			continue
		}
		open++
		if oldest == nil || *a.TransactionAgeSeconds > *oldest {
			oldest = a.TransactionAgeSeconds
		}
		if report.IsIdleInTransaction(s.State) && (oldestIdle == nil || *a.TransactionAgeSeconds > *oldestIdle) {
			oldestIdle = a.TransactionAgeSeconds
		}
	}
	// With no waiter or no open transaction the maximum is undefined, so it
	// is stored as NULL rather than 0.
	pts = append(pts,
		observe.Point{Metric: "blocking.max_lock_wait_seconds", Value: maxWait},
		observe.Point{Metric: "transactions.open", Value: f(open)},
		observe.Point{Metric: "transactions.oldest_age_seconds", Value: oldest},
		observe.Point{Metric: "transactions.idle_in_transaction_oldest_age_seconds", Value: oldestIdle},
	)
	return pts
}

func (w *worker) collectDatabase(ctx context.Context) error {
	if err := w.refreshCapabilities(ctx); err != nil {
		return err
	}
	var cur CounterSample
	if err := w.obs.ReadOnly(ctx, func(ctx context.Context, tx pgx.Tx, _ []int32) error {
		var err error
		cur, err = readDatabaseCounters(ctx, tx, *w.caps)
		return err
	}); err != nil {
		return err
	}
	cur.At = time.Now().UTC()
	maxGap := 3 * w.jobByName(CollectorDatabase).every
	points, reasons := deriveDatabasePoints(w.prevDB, cur, w.caps.IsStandby, maxGap)
	prev := w.prevDB
	w.prevDB = &cur
	w.state.addPoints(cur.At, points)
	id, at := w.target.ID, cur.At
	w.m.writer.Enqueue(WriteOp{TargetID: id, Collector: CollectorDatabase, At: at,
		Write: func(ctx context.Context, s *storage.Store, _ storage.IdentityCache) error {
			return s.InsertPoints(ctx, id, at, points)
		}})
	if prev != nil && (slices.Contains(reasons, ReasonRestart) || slices.Contains(reasons, ReasonStatsReset) || slices.Contains(reasons, ReasonGap)) {
		from := prev.At
		reason := "counter rates unknown: " + strings.Join(reasons, ", ")
		w.m.writer.Enqueue(WriteOp{TargetID: id, Collector: CollectorDatabase, At: at,
			Write: func(ctx context.Context, s *storage.Store, _ storage.IdentityCache) error {
				return s.InsertGap(ctx, id, CollectorDatabase, from, at, reason)
			}})
	}
	return nil
}

func (w *worker) collectStatements(ctx context.Context) error {
	var (
		entries map[observe.QueryKey]statementCounters
		meta    map[observe.QueryKey][4]*string
		hidden  int
		dealloc *int64
		reset   *time.Time
	)
	if err := w.obs.ReadOnly(ctx, func(ctx context.Context, tx pgx.Tx, _ []int32) error {
		var err error
		if entries, meta, hidden, err = readStatements(ctx, tx, *w.caps, w.target.QueryTextEnabled); err != nil {
			return err
		}
		dealloc, reset, err = readStatementsInfo(ctx, tx, *w.caps)
		return err
	}); err != nil {
		return err
	}
	cur := statementsState{at: time.Now().UTC(), statsReset: reset, dealloc: dealloc, entries: entries}
	coll := diffStatements(w.prevStmt, cur, meta, hidden)
	w.prevStmt = &cur

	var pts []observe.Point
	if coll.Interval != nil && *coll.Interval > 0 {
		var calls int64
		var ms float64
		for _, d := range coll.Deltas {
			calls += d.Calls
			ms += d.TotalMS
		}
		if coll.Truncated > 0 || (coll.Dealloc != nil && *coll.Dealloc > 0) {
			// Part of the activity is not in the stored deltas.
			pts = append(pts, observe.Point{Metric: "statements.calls.per_s"}, observe.Point{Metric: "statements.exec_ms.per_s"})
		} else {
			pts = append(pts,
				observe.Point{Metric: "statements.calls.per_s", IntervalSeconds: coll.Interval, Value: observe.F(float64(calls) / *coll.Interval)},
				observe.Point{Metric: "statements.exec_ms.per_s", IntervalSeconds: coll.Interval, Value: observe.F(ms / *coll.Interval)})
		}
	}
	w.state.mu.Lock()
	h := append(w.state.statements, coll)
	if len(h) > maxStatementHistory {
		h = h[len(h)-maxStatementHistory:]
	}
	w.state.statements = append([]observe.StatementsCollection(nil), h...)
	w.state.mu.Unlock()
	w.state.addPoints(coll.At, pts)

	id, at, text := w.target.ID, coll.At, w.target.QueryTextEnabled
	w.m.writer.Enqueue(WriteOp{TargetID: id, Collector: CollectorStatements, At: at,
		Write: func(ctx context.Context, s *storage.Store, cache storage.IdentityCache) error {
			if err := s.InsertStatements(ctx, id, coll, text, cache); err != nil {
				return err
			}
			return s.InsertPoints(ctx, id, at, pts)
		}})
	return nil
}

func (w *worker) collectReplication(ctx context.Context) error {
	var r observe.Replication
	if err := w.obs.ReadOnly(ctx, func(ctx context.Context, tx pgx.Tx, _ []int32) error {
		var err error
		r, err = readReplication(ctx, tx, *w.caps)
		return err
	}); err != nil {
		return err
	}
	pts := replicationPoints(r)
	w.state.mu.Lock()
	w.state.replication = &r
	w.state.mu.Unlock()
	w.state.addPoints(r.At, pts)
	raw, err := json.Marshal(r)
	if err != nil {
		return err
	}
	id, at := w.target.ID, r.At
	w.m.writer.Enqueue(WriteOp{TargetID: id, Collector: CollectorReplication, At: at,
		Write: func(ctx context.Context, s *storage.Store, _ storage.IdentityCache) error {
			if err := s.InsertStateSnapshot(ctx, id, "replication", at, raw); err != nil {
				return err
			}
			return s.InsertPoints(ctx, id, at, pts)
		}})
	return nil
}

func (w *worker) collectTables(ctx context.Context) error {
	var h observe.TableHealth
	if err := w.obs.ReadOnly(ctx, func(ctx context.Context, tx pgx.Tx, _ []int32) error {
		var err error
		h, err = readTableHealth(ctx, tx)
		return err
	}); err != nil {
		return err
	}
	var pts []observe.Point
	var total float64
	known := true
	for _, d := range h.Databases {
		var size *float64
		if d.SizeBytes != nil {
			size = observe.F(float64(*d.SizeBytes))
			total += *size
		} else {
			known = false
		}
		var age *float64
		if d.XIDAge != nil {
			age = observe.F(float64(*d.XIDAge))
		}
		pts = append(pts, observe.Point{Metric: "db.size_bytes", Label: d.DatName, Value: size},
			observe.Point{Metric: "db.xid_age", Label: d.DatName, Value: age})
	}
	if known && len(h.Databases) > 0 {
		pts = append(pts, observe.Point{Metric: "db.total_size_bytes", Value: observe.F(total)})
	} else {
		pts = append(pts, observe.Point{Metric: "db.total_size_bytes"})
	}
	w.state.mu.Lock()
	w.state.tables = &h
	w.state.mu.Unlock()
	w.state.addPoints(h.At, pts)
	id, at := w.target.ID, h.At
	w.m.writer.Enqueue(WriteOp{TargetID: id, Collector: CollectorTables, At: at,
		Write: func(ctx context.Context, s *storage.Store, _ storage.IdentityCache) error {
			if err := s.InsertTableHealth(ctx, id, h); err != nil {
				return err
			}
			return s.InsertPoints(ctx, id, at, pts)
		}})
	return nil
}

func (w *worker) collectHost(ctx context.Context) error {
	scrape, err := hostmetrics.Fetch(ctx, w.httpClient, *w.target.NodeExporterURL)
	if err != nil {
		msg := sanitizeError(err)
		w.state.mu.Lock()
		w.state.hostError = &msg
		w.state.mu.Unlock()
		return err
	}
	var interval *float64
	if w.prevHost != nil && scrape.At.After(w.prevHost.At) {
		interval = observe.F(scrape.At.Sub(w.prevHost.At).Seconds())
	}
	d := hostmetrics.Derive(w.prevHost, scrape)
	w.prevHost = &scrape
	hm := observe.HostMetrics{At: scrape.At, CPUUtilization: d.CPUUtilization, MemTotalBytes: d.MemTotal,
		MemAvailBytes: d.MemAvailable, DiskReadBps: d.DiskReadBps, DiskWriteBps: d.DiskWriteBps, NetRxBps: d.NetRxBps, NetTxBps: d.NetTxBps}
	pts := []observe.Point{
		{Metric: "host.cpu_utilization_ratio", Value: d.CPUUtilization},
		{Metric: "host.memory_available_bytes", Value: d.MemAvailable},
		{Metric: "host.memory_total_bytes", Value: d.MemTotal},
		{Metric: "host.disk_read_bytes.per_s", IntervalSeconds: interval, Value: d.DiskReadBps},
		{Metric: "host.disk_write_bytes.per_s", IntervalSeconds: interval, Value: d.DiskWriteBps},
		{Metric: "host.network_receive_bytes.per_s", IntervalSeconds: interval, Value: d.NetRxBps},
		{Metric: "host.network_transmit_bytes.per_s", IntervalSeconds: interval, Value: d.NetTxBps},
	}
	for _, fs := range d.Filesystems {
		hm.Filesystems = append(hm.Filesystems, observe.Filesystem{Mountpoint: fs.Mountpoint, FSType: fs.FSType, SizeBytes: fs.Size, AvailBytes: fs.Avail})
		pts = append(pts, observe.Point{Metric: "host.filesystem_used_ratio", Label: fs.Mountpoint, Value: observe.F(1 - fs.Avail/fs.Size)})
	}
	w.state.mu.Lock()
	w.state.host, w.state.hostError = &hm, nil
	w.state.mu.Unlock()
	w.state.addPoints(hm.At, pts)
	id, at := w.target.ID, hm.At
	w.m.writer.Enqueue(WriteOp{TargetID: id, Collector: CollectorHost, At: at,
		Write: func(ctx context.Context, s *storage.Store, _ storage.IdentityCache) error {
			return s.InsertPoints(ctx, id, at, pts)
		}})
	return nil
}

func (w *worker) jobByName(name string) *job {
	for _, j := range w.jobs {
		if j.name == name {
			return j
		}
	}
	return nil
}

// isConnectionError reports failures that mean the connection or the server
// is gone, after which the worker reconnects.
func isConnectionError(err error) bool {
	var ce *pgconn.ConnectError
	var ne net.Error
	switch {
	case errors.As(err, &ce), errors.Is(err, pgconn.ErrConnClosed), errors.Is(err, io.EOF), errors.Is(err, net.ErrClosed):
		return true
	case errors.As(err, &ne):
		return true
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		// admin_shutdown, crash_shutdown, cannot_connect_now
		return pgErr.Code == "57P01" || pgErr.Code == "57P02" || pgErr.Code == "57P03"
	}
	msg := err.Error()
	return strings.Contains(msg, "conn closed") || strings.Contains(msg, "connection reset") ||
		strings.Contains(msg, "broken pipe") || strings.Contains(msg, "acquire connection")
}

// sanitizeError returns a single-line error message for storage and the UI.
// Errors from postgres.Connect and the collectors are already redacted; this
// additionally bounds length and strips line breaks.
func sanitizeError(err error) string {
	msg := strings.Join(strings.Fields(err.Error()), " ")
	if len(msg) > 500 {
		msg = msg[:500] + "…"
	}
	if hint := postgres.Hint(err); hint != "" {
		msg += " (hint: " + hint + ")"
	}
	return msg
}

// DSNAdditionalData binds encrypted DSNs to their purpose.
const DSNAdditionalData = "locklens-target-dsn-v1"
