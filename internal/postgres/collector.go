// Package postgres connects to PostgreSQL as a read-only observer and
// collects lock contention snapshots.
//
// The relationships between blockers and waiters come exclusively from
// pg_blocking_pids(). pg_locks is used only to find which backends are waiting
// and to describe what they wait for; LockLens never infers a conflict by
// matching lock targets, because that ignores lock mode compatibility and
// wait-queue order. See docs/postgresql-observation.md.
package postgres

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/locklens/locklens/internal/config"
	"github.com/locklens/locklens/internal/model"
	"github.com/locklens/locklens/internal/textsafe"
)

//go:embed sql/*.sql
var sqlFiles embed.FS

func mustSQL(name string) string {
	b, err := sqlFiles.ReadFile("sql/" + name)
	if err != nil {
		panic(err)
	}
	return string(b)
}

var (
	sqlServer       = mustSQL("server.sql")
	sqlBlocking     = mustSQL("blocking.sql")
	sqlWaitingLocks = mustSQL("waiting_locks.sql")
	sqlSessions     = mustSQL("sessions.sql")
	sqlPrepared     = mustSQL("prepared.sql")
)

// MinServerVersionNum is the oldest supported server (PostgreSQL 14). Every
// column LockLens reads exists from this version on, notably pg_locks.waitstart.
const MinServerVersionNum = 140000

// UnsupportedVersionError is returned for servers older than PostgreSQL 14.
type UnsupportedVersionError struct {
	Version string
}

func (e *UnsupportedVersionError) Error() string {
	return fmt.Sprintf("PostgreSQL %s is not supported; LockLens requires PostgreSQL 14 or later", e.Version)
}

// Collector owns a small connection pool and collects snapshots.
// It is safe for sequential use; callers must not run collections
// concurrently (watch never does).
type Collector struct {
	pool   *pgxpool.Pool
	own    *pidSet
	target config.Target
	red    *config.Redactor
	tool   model.Tool
}

// pidSet tracks backend PIDs of the pool's own connections. Pool hooks run on
// pool goroutines, hence the mutex.
type pidSet struct {
	mu   sync.Mutex
	pids map[int32]struct{}
}

func (s *pidSet) add(pid uint32) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pids[int32(pid)] = struct{}{}
}

func (s *pidSet) remove(pid uint32) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.pids, int32(pid))
}

func (s *pidSet) list() []int32 {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]int32, 0, len(s.pids))
	for p := range s.pids {
		out = append(out, p)
	}
	slices.Sort(out)
	return out
}

func (s *pidSet) has(pid int) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.pids[int32(pid)]
	return ok
}

// Connect creates the pool and verifies that a connection can be made.
// Errors are passed through the redactor before being returned.
func Connect(ctx context.Context, cfg *pgxpool.Config, red *config.Redactor, tool model.Tool) (*Collector, error) {
	own := &pidSet{pids: map[int32]struct{}{}}
	// The backend PID of each connection is known from the startup handshake
	// (BackendKeyData), so LockLens can exclude every one of its own sessions
	// without relying on application_name, which any client could also use.
	cfg.AfterConnect = func(_ context.Context, c *pgx.Conn) error {
		own.add(c.PgConn().PID())
		return nil
	}
	cfg.BeforeClose = func(c *pgx.Conn) {
		own.remove(c.PgConn().PID())
	}
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, red.Error(err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, red.Error(err)
	}
	return &Collector{pool: pool, own: own, target: config.TargetOf(cfg), red: red, tool: tool}, nil
}

// Close releases all connections.
func (c *Collector) Close() {
	c.pool.Close()
}

// Target returns the non-secret connection target.
func (c *Collector) Target() config.Target { return c.target }

// Options control what a collection includes.
type Options struct {
	IncludeQuery   bool
	QueryMaxLength int
	MinTxAge       time.Duration
}

type serverInfo struct {
	versionNum      int
	version         string
	database        string
	readAllStats    bool
	trackActivities bool
}

// Collect gathers one snapshot. All queries run in a single read-only
// transaction on one connection; pg_stat_activity is therefore read once and
// cached for the transaction, while pg_locks and pg_blocking_pids() are read
// live. The result is close in time but not atomic.
func (c *Collector) Collect(ctx context.Context, opts Options) (*model.Snapshot, error) {
	snap, err := c.collect(ctx, opts)
	if err != nil {
		return nil, c.red.Error(err)
	}
	return snap, nil
}

func (c *Collector) collect(ctx context.Context, opts Options) (*model.Snapshot, error) {
	started := time.Now()
	conn, err := c.pool.Acquire(ctx)
	if err != nil {
		return nil, fmt.Errorf("acquire connection: %w", err)
	}
	defer conn.Release()

	tx, err := conn.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, fmt.Errorf("begin read-only transaction: %w", err)
	}
	// Nothing is ever written; rolling back simply ends the transaction.
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

	own := c.own.list()

	srv, err := readServer(ctx, tx)
	if err != nil {
		return nil, err
	}
	if srv.versionNum < MinServerVersionNum {
		return nil, &UnsupportedVersionError{Version: srv.version}
	}

	snap := &model.Snapshot{
		SchemaVersion: model.SchemaVersion,
		Tool:          c.tool,
		Collection: model.Collection{
			IncludeQuery:    opts.IncludeQuery,
			QueryMaxLength:  opts.QueryMaxLength,
			MinTxAgeSeconds: opts.MinTxAge.Seconds(),
		},
		Server: model.Server{
			Host:            c.target.Host,
			Port:            c.target.Port,
			Database:        srv.database,
			User:            c.target.User,
			Version:         srv.version,
			VersionNum:      srv.versionNum,
			ReadAllStats:    srv.readAllStats,
			TrackActivities: srv.trackActivities,
		},
		Sessions:             []model.Session{},
		Edges:                []model.Edge{},
		PreparedTransactions: []model.PreparedTransaction{},
		Warnings:             []model.Warning{},
	}

	edges, involved, err := c.readBlocking(ctx, tx, own, snap)
	if err != nil {
		return nil, err
	}
	snap.Edges = edges

	locks, err := readWaitingLocks(ctx, tx, own)
	if err != nil {
		return nil, err
	}

	sessions, err := readSessions(ctx, tx, own, involved, opts)
	if err != nil {
		return nil, err
	}
	for i := range sessions {
		sessions[i].WaitingLocks = nonNil(locks[sessions[i].PID])
	}
	snap.Sessions = sessions

	prepared, err := readPrepared(ctx, tx)
	if err != nil {
		return nil, err
	}
	snap.PreparedTransactions = prepared

	// Ages are measured against a server timestamp taken after every source
	// was read, so nothing observed can appear to start in the future.
	var end time.Time
	if err := tx.QueryRow(ctx, "SELECT clock_timestamp()").Scan(&end); err != nil {
		return nil, fmt.Errorf("read server clock: %w", err)
	}
	snap.ServerTime = end.UTC()
	snap.CollectedAt = time.Now().UTC()
	snap.Collection.DurationMS = time.Since(started).Milliseconds()
	addVisibilityWarnings(snap)
	return snap, nil
}

func readServer(ctx context.Context, tx pgx.Tx) (serverInfo, error) {
	var s serverInfo
	err := tx.QueryRow(ctx, sqlServer).Scan(&s.versionNum, &s.version, &s.database, &s.readAllStats, &s.trackActivities)
	if err != nil {
		return s, fmt.Errorf("read server information: %w", err)
	}
	return s, nil
}

// readBlocking returns deduplicated edges and the set of PIDs taking part in
// them.
func (c *Collector) readBlocking(ctx context.Context, tx pgx.Tx, own []int32, snap *model.Snapshot) ([]model.Edge, []int32, error) {
	rows, err := tx.Query(ctx, sqlBlocking, own)
	if err != nil {
		return nil, nil, fmt.Errorf("read blocking relations: %w", err)
	}
	type pair struct{ blocker, waiter int }
	seen := map[pair]bool{}
	involvedSet := map[int32]bool{}
	edges := []model.Edge{}
	droppedOwn := false
	var waiter int32
	var blockers []int32
	_, err = pgx.ForEachRow(rows, []any{&waiter, &blockers}, func() error {
		for _, b := range blockers {
			// A LockLens connection should never hold a conflicting lock, but if
			// one ever does, do not present our own session as a culprit.
			if c.own.has(int(b)) {
				droppedOwn = true
				continue
			}
			p := pair{int(b), int(waiter)}
			if seen[p] {
				continue // duplicate PID from parallel workers
			}
			seen[p] = true
			edges = append(edges, model.Edge{Blocker: p.blocker, Waiter: p.waiter})
			involvedSet[waiter] = true
			if b != 0 {
				involvedSet[b] = true
			}
		}
		return nil
	})
	if err != nil {
		return nil, nil, fmt.Errorf("read blocking relations: %w", err)
	}
	if droppedOwn {
		snap.Warnings = append(snap.Warnings, model.Warning{
			Code:    model.WarnSelfBlocking,
			Message: "a LockLens connection appeared as a blocker and was left out of the graph",
		})
	}
	slices.SortFunc(edges, func(a, b model.Edge) int {
		if a.Blocker != b.Blocker {
			return a.Blocker - b.Blocker
		}
		return a.Waiter - b.Waiter
	})
	involved := make([]int32, 0, len(involvedSet))
	for p := range involvedSet {
		involved = append(involved, p)
	}
	slices.Sort(involved)
	return edges, involved, nil
}

func readWaitingLocks(ctx context.Context, tx pgx.Tx, own []int32) (map[int][]model.WaitingLock, error) {
	rows, err := tx.Query(ctx, sqlWaitingLocks, own)
	if err != nil {
		return nil, fmt.Errorf("read waiting locks: %w", err)
	}
	out := map[int][]model.WaitingLock{}
	var (
		l          model.WaitingLock
		sessionPID int
	)
	_, err = pgx.ForEachRow(rows, []any{
		&l.PID, &sessionPID, &l.LockType, &l.Mode, &l.Database, &l.RelationOID, &l.Relation,
		&l.Page, &l.Tuple, &l.VirtualXID, &l.TransactionID, &l.ClassID, &l.ObjID, &l.ObjSubID, &l.WaitStart,
	}, func() error {
		lock := l
		lock.WaitStart = utcPtr(lock.WaitStart)
		out[sessionPID] = append(out[sessionPID], lock)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("read waiting locks: %w", err)
	}
	return out, nil
}

func readSessions(ctx context.Context, tx pgx.Tx, own, involved []int32, opts Options) ([]model.Session, error) {
	rows, err := tx.Query(ctx, sqlSessions, own, involved, opts.IncludeQuery, opts.QueryMaxLength)
	if err != nil {
		return nil, fmt.Errorf("read pg_stat_activity: %w", err)
	}
	sessions := []model.Session{}
	var (
		s              model.Session
		queryTruncated *bool
	)
	_, err = pgx.ForEachRow(rows, []any{
		&s.PID, &s.LeaderPID, &s.Database, &s.User, &s.ApplicationName, &s.ClientAddr, &s.BackendType,
		&s.State, &s.WaitEventType, &s.WaitEvent, &s.BackendStart, &s.XactStart, &s.QueryStart,
		&s.StateChange, &s.BackendXID, &s.Query, &queryTruncated, &s.DetailsVisible, &s.ParallelWorkers,
	}, func() error {
		sess := s
		sess.BackendStart = utcPtr(sess.BackendStart)
		sess.XactStart = utcPtr(sess.XactStart)
		sess.QueryStart = utcPtr(sess.QueryStart)
		sess.StateChange = utcPtr(sess.StateChange)
		sess.QueryTruncated = false
		switch {
		case !opts.IncludeQuery || !sess.DetailsVisible || sess.Query == nil:
			// Hidden text arrives as '<insufficient privilege>'; never store it
			// as if it were the query.
			sess.Query = nil
		default:
			// The server already cut the text by characters; truncating again
			// by runes guarantees the limit and valid UTF-8 on our side.
			q, cut := textsafe.Truncate(*sess.Query, opts.QueryMaxLength)
			sess.Query = &q
			sess.QueryTruncated = cut || (queryTruncated != nil && *queryTruncated)
		}
		sessions = append(sessions, sess)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("read pg_stat_activity: %w", err)
	}
	return sessions, nil
}

func readPrepared(ctx context.Context, tx pgx.Tx) ([]model.PreparedTransaction, error) {
	rows, err := tx.Query(ctx, sqlPrepared)
	if err != nil {
		return nil, fmt.Errorf("read pg_prepared_xacts: %w", err)
	}
	out, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (model.PreparedTransaction, error) {
		var p model.PreparedTransaction
		err := r.Scan(&p.GID, &p.Transaction, &p.Prepared, &p.Owner, &p.Database)
		p.Prepared = p.Prepared.UTC()
		return p, err
	})
	if err != nil {
		return nil, fmt.Errorf("read pg_prepared_xacts: %w", err)
	}
	return nonNil(out), nil
}

func addVisibilityWarnings(snap *model.Snapshot) {
	hidden := 0
	for _, s := range snap.Sessions {
		if !s.DetailsVisible {
			hidden++
		}
	}
	switch {
	case hidden > 0:
		snap.Warnings = append(snap.Warnings, model.Warning{
			Code: model.WarnRestrictedVisibility,
			Message: fmt.Sprintf("details of %d session(s) are hidden from role %q; state, timings, and query text are unknown for them, "+
				"and long-running transactions of other roles cannot be detected. Grant pg_read_all_stats (or pg_monitor) for a complete report",
				hidden, snap.Server.User),
		})
	case !snap.Server.ReadAllStats:
		snap.Warnings = append(snap.Warnings, model.Warning{
			Code: model.WarnRestrictedVisibility,
			Message: fmt.Sprintf("role %q lacks pg_read_all_stats: transactions of other roles are invisible unless they take part in blocking, "+
				"so the long-running transaction list may be incomplete", snap.Server.User),
		})
	}
	if !snap.Server.TrackActivities {
		snap.Warnings = append(snap.Warnings, model.Warning{
			Code:    model.WarnTrackActivitiesOff,
			Message: "track_activities is off: session state, wait events, and query text are not maintained by the server",
		})
	}
}

func utcPtr(t *time.Time) *time.Time {
	if t == nil {
		return nil
	}
	u := t.UTC()
	return &u
}

func nonNil[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}

// IsFatal reports whether retrying a failed collection is pointless, such as
// for authentication failures or an unsupported server.
func IsFatal(err error) bool {
	var uv *UnsupportedVersionError
	if errors.As(err, &uv) {
		return true
	}
	switch sqlState(err) {
	case "28000", "28P01", "3D000", "42501":
		return true
	}
	return false
}
