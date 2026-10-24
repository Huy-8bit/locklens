// Package scenario creates real lock contention in a PostgreSQL database for
// the LockLens demo and integration tests.
//
// This package WRITES to the database and is intentionally separate from the
// observer code: the locklens CLI never imports it (enforced by
// `make check-separation`). Use it only against disposable local databases.
//
// Synchronisation never relies on fixed sleeps. After starting a statement
// that is expected to block, the scenario polls pg_blocking_pids() (or
// pg_stat_activity for idle sessions) until the expected state is visible or a
// timeout expires.
package scenario

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"
)

// AppPrefix is the application_name prefix of scenario connections, so they
// can be recognised (and cleaned up) by tools such as locklens-lab.
var AppPrefix = "locklens-demo-"

// PollInterval and WaitTimeout bound the state polling.
var (
	PollInterval = 25 * time.Millisecond
	WaitTimeout  = 15 * time.Second
)

// Session is one demo connection holding an open transaction.
type Session struct {
	Label string
	PID   int
	// TxStart is now() inside the session's transaction, i.e. the value
	// PostgreSQL reports as xact_start. Tests use it to check accuracy.
	TxStart time.Time

	conn    *pgx.Conn
	tx      pgx.Tx
	pending chan error // non-nil while a statement runs in the background
	cancel  context.CancelFunc
}

// Scenario owns the sessions of one contention setup.
type Scenario struct {
	Name     string
	Table    string
	Sessions []*Session
	dsn      string
	monitor  *pgx.Conn
	// prepared lists GIDs of prepared transactions to roll back on Close.
	prepared []string
}

// ByLabel returns the session with the given label.
func (s *Scenario) ByLabel(label string) *Session {
	for _, sess := range s.Sessions {
		if sess.Label == label {
			return sess
		}
	}
	return nil
}

// New connects a monitor session and (re)creates table with synthetic rows
// (ids 1 to 10). Use Begin, Exec, and ExecAsync to build custom contention.
func New(ctx context.Context, name, dsn, table string) (*Scenario, error) {
	return newScenario(ctx, name, dsn, table)
}

func newScenario(ctx context.Context, name, dsn, table string) (*Scenario, error) {
	mon, err := connect(ctx, dsn, AppPrefix+"monitor")
	if err != nil {
		return nil, err
	}
	s := &Scenario{Name: name, Table: table, dsn: dsn, monitor: mon}
	if err := s.prepareTable(ctx); err != nil {
		_ = mon.Close(ctx)
		return nil, err
	}
	return s, nil
}

func connect(ctx context.Context, dsn, appName string) (*pgx.Conn, error) {
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		return nil, errors.New("scenario: invalid DSN")
	}
	rp := cfg.RuntimeParams
	rp["application_name"] = appName
	// Safety nets: if the orchestrator dies, the server still ends these
	// transactions eventually.
	rp["lock_timeout"] = "300000"
	rp["idle_in_transaction_session_timeout"] = "600000"
	conn, err := pgx.ConnectConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("scenario: connect: %w", err)
	}
	return conn, nil
}

// Ident returns the quoted table name for use in SQL text.
func (s *Scenario) Ident() string { return pgx.Identifier{s.Table}.Sanitize() }

func (s *Scenario) ident() string { return s.Ident() }

// prepareTable (re)creates the demo table with fake data.
func (s *Scenario) prepareTable(ctx context.Context) error {
	sql := fmt.Sprintf(`
CREATE TABLE IF NOT EXISTS %[1]s (
    id      int PRIMARY KEY,
    owner   text NOT NULL,
    balance numeric(12,2) NOT NULL
);
TRUNCATE %[1]s;
INSERT INTO %[1]s (id, owner, balance)
SELECT g, 'customer-' || g, 100 * g FROM generate_series(1, 10) AS g;`, s.ident())
	if _, err := s.monitor.Exec(ctx, sql); err != nil {
		return fmt.Errorf("scenario: prepare table: %w", err)
	}
	return nil
}

// Begin opens a dedicated connection for an actor and starts a transaction.
// The actor's PID is read from the connection itself.
func (s *Scenario) Begin(ctx context.Context, label string) (*Session, error) {
	return s.begin(ctx, label)
}

func (s *Scenario) begin(ctx context.Context, label string) (*Session, error) {
	conn, err := connect(ctx, s.dsn, AppPrefix+label)
	if err != nil {
		return nil, err
	}
	tx, err := conn.Begin(ctx)
	if err != nil {
		_ = conn.Close(ctx)
		return nil, fmt.Errorf("scenario: begin: %w", err)
	}
	sess := &Session{Label: label, conn: conn, tx: tx, PID: int(conn.PgConn().PID())}
	if err := tx.QueryRow(ctx, "SELECT now()").Scan(&sess.TxStart); err != nil {
		_ = conn.Close(ctx)
		return nil, fmt.Errorf("scenario: read transaction start: %w", err)
	}
	s.Sessions = append(s.Sessions, sess)
	return sess, nil
}

// Exec runs sql inside the actor's transaction and waits for it.
func (s *Scenario) Exec(ctx context.Context, sess *Session, sql string, args ...any) error {
	if sess.pending != nil {
		return fmt.Errorf("scenario: session %s already has a statement running", sess.Label)
	}
	if _, err := sess.tx.Exec(ctx, sql, args...); err != nil {
		return fmt.Errorf("scenario: session %s: %w", sess.Label, err)
	}
	return nil
}

// ExecAsync starts sql inside the actor's transaction without waiting, for a
// statement that is expected to block. Use WaitBlocked to confirm the wait
// and WaitDone to collect the result.
func (s *Scenario) ExecAsync(sess *Session, sql string, args ...any) {
	ctx, cancel := context.WithCancel(context.Background())
	sess.cancel = cancel
	sess.pending = make(chan error, 1)
	go func() {
		_, err := sess.tx.Exec(ctx, sql, args...)
		if err != nil {
			err = fmt.Errorf("scenario: session %s: %w", sess.Label, err)
		}
		sess.pending <- err
	}()
}

// WaitDone waits for the actor's background statement to finish and returns
// its error.
func (s *Scenario) WaitDone(ctx context.Context, sess *Session) error {
	if sess.pending == nil {
		return nil
	}
	select {
	case err := <-sess.pending:
		sess.pending = nil
		sess.cancel()
		return err
	case <-ctx.Done():
		return fmt.Errorf("scenario: session %s statement did not finish: %w", sess.Label, ctx.Err())
	}
}

// Release rolls back the actor's transaction, releasing its locks. The
// connection stays open until Close.
func (s *Scenario) Release(ctx context.Context, sess *Session) error {
	// A failed background statement does not prevent the rollback; only a
	// statement that is still running does.
	if err := s.WaitDone(ctx, sess); err != nil && sess.pending != nil {
		return err
	}
	if sess.tx == nil {
		return nil
	}
	err := sess.tx.Rollback(ctx)
	sess.tx = nil
	if err != nil {
		return fmt.Errorf("scenario: release %s: %w", sess.Label, err)
	}
	return nil
}

func (s *Scenario) updateRow(ctx context.Context, sess *Session, id int) error {
	return s.Exec(ctx, sess, fmt.Sprintf("UPDATE %s SET balance = balance + 1 WHERE id = $1", s.ident()), id)
}

// updateRowAsync starts an UPDATE expected to block and returns immediately.
func (s *Scenario) updateRowAsync(sess *Session, id int) {
	s.ExecAsync(sess, fmt.Sprintf("UPDATE %s SET balance = balance + 1 WHERE id = $1", s.ident()), id)
}

// WaitBlocked polls until pg_blocking_pids(waiter) contains every expected
// blocker PID.
func (s *Scenario) WaitBlocked(ctx context.Context, waiter int, blockers ...int) error {
	return s.poll(ctx, fmt.Sprintf("pid %d blocked by %v", waiter, blockers), func() (bool, error) {
		var got []int32
		if err := s.monitor.QueryRow(ctx, "SELECT pg_blocking_pids($1)", waiter).Scan(&got); err != nil {
			return false, err
		}
		for _, b := range blockers {
			if !slices.Contains(got, int32(b)) {
				return false, nil
			}
		}
		return true, nil
	})
}

// WaitIdleInTransaction polls until pid is idle in transaction and its
// transaction is at least minAge old according to the server clock.
func (s *Scenario) WaitIdleInTransaction(ctx context.Context, pid int, minAge time.Duration) error {
	return s.poll(ctx, fmt.Sprintf("pid %d idle in transaction for %s", pid, minAge), func() (bool, error) {
		var ok bool
		err := s.monitor.QueryRow(ctx, `
SELECT coalesce(bool_or(state = 'idle in transaction'
                        AND clock_timestamp() - xact_start >= $2::interval), false)
FROM pg_stat_activity WHERE pid = $1`, pid, minAge).Scan(&ok)
		return ok, err
	})
}

func (s *Scenario) poll(ctx context.Context, what string, cond func() (bool, error)) error {
	ctx, cancel := context.WithTimeout(ctx, WaitTimeout)
	defer cancel()
	t := time.NewTicker(PollInterval)
	defer t.Stop()
	for {
		ok, err := cond()
		if err != nil && ctx.Err() == nil {
			return fmt.Errorf("scenario: waiting for %s: %w", what, err)
		}
		if ok {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("scenario: timed out after %s waiting for %s", WaitTimeout, what)
		case <-t.C:
		}
	}
}

// Close ends every transaction and connection. Sessions are released in
// creation order: releasing a holder lets its waiter's statement finish, which
// is then rolled back, which in turn releases the next waiter. A statement
// that does not finish in time is cancelled, and a connection that cannot be
// rolled back cleanly is closed, which makes the server abort its transaction.
func (s *Scenario) Close(ctx context.Context) error {
	var errs []error
	for _, gid := range s.prepared {
		if _, err := s.monitor.Exec(ctx, "ROLLBACK PREPARED "+quoteLiteral(gid)); err != nil {
			errs = append(errs, fmt.Errorf("rollback prepared %s: %w", gid, err))
		}
	}
	for _, sess := range s.Sessions {
		if sess.pending != nil {
			select {
			case <-sess.pending:
			case <-time.After(5 * time.Second):
				sess.cancel()
				<-sess.pending
			}
			sess.cancel()
			sess.pending = nil
		}
		if sess.tx != nil {
			_ = sess.tx.Rollback(ctx)
		}
		if err := sess.conn.Close(ctx); err != nil {
			errs = append(errs, err)
		}
	}
	if err := s.monitor.Close(ctx); err != nil {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

func quoteLiteral(s string) string {
	out := []byte{'\''}
	for i := 0; i < len(s); i++ {
		if s[i] == '\'' {
			out = append(out, '\'')
		}
		out = append(out, s[i])
	}
	return string(append(out, '\''))
}

// RowLock: A updates row 1 and keeps its transaction open; B updates the same
// row and blocks. Expected graph: A → B.
func RowLock(ctx context.Context, dsn, table string) (s *Scenario, err error) {
	s, err = newScenario(ctx, "row-lock", dsn, table)
	if err != nil {
		return nil, err
	}
	defer closeOnError(ctx, s, &err)

	a, err := s.begin(ctx, "A")
	if err != nil {
		return nil, err
	}
	if err = s.updateRow(ctx, a, 1); err != nil {
		return nil, err
	}
	b, err := s.begin(ctx, "B")
	if err != nil {
		return nil, err
	}
	s.updateRowAsync(b, 1)
	if err = s.WaitBlocked(ctx, b.PID, a.PID); err != nil {
		return nil, err
	}
	return s, nil
}

// Chain: A holds row 1; B holds row 2 and waits for row 1; C waits for row 2.
// Expected graph: A → B → C (A blocks 1 directly, 2 transitively).
func Chain(ctx context.Context, dsn, table string) (s *Scenario, err error) {
	s, err = newScenario(ctx, "chain", dsn, table)
	if err != nil {
		return nil, err
	}
	defer closeOnError(ctx, s, &err)

	a, err := s.begin(ctx, "A")
	if err != nil {
		return nil, err
	}
	if err = s.updateRow(ctx, a, 1); err != nil {
		return nil, err
	}
	b, err := s.begin(ctx, "B")
	if err != nil {
		return nil, err
	}
	if err = s.updateRow(ctx, b, 2); err != nil {
		return nil, err
	}
	s.updateRowAsync(b, 1)
	if err = s.WaitBlocked(ctx, b.PID, a.PID); err != nil {
		return nil, err
	}
	c, err := s.begin(ctx, "C")
	if err != nil {
		return nil, err
	}
	s.updateRowAsync(c, 2)
	if err = s.WaitBlocked(ctx, c.PID, b.PID); err != nil {
		return nil, err
	}
	return s, nil
}

// IdleInTransaction: A updates a row, then stays idle inside its open
// transaction until it is at least minAge old.
func IdleInTransaction(ctx context.Context, dsn, table string, minAge time.Duration) (s *Scenario, err error) {
	s, err = newScenario(ctx, "idle-in-transaction", dsn, table)
	if err != nil {
		return nil, err
	}
	defer closeOnError(ctx, s, &err)

	a, err := s.begin(ctx, "A")
	if err != nil {
		return nil, err
	}
	if err = s.updateRow(ctx, a, 3); err != nil {
		return nil, err
	}
	if err = s.WaitIdleInTransaction(ctx, a.PID, minAge); err != nil {
		return nil, err
	}
	return s, nil
}

// PreparedBlocker: A updates row 4 and runs PREPARE TRANSACTION, after which
// the lock belongs to a prepared transaction with no backend; B then blocks on
// row 4. pg_blocking_pids(B) reports PID 0. Requires
// max_prepared_transactions > 0.
func PreparedBlocker(ctx context.Context, dsn, table, gid string) (s *Scenario, err error) {
	s, err = newScenario(ctx, "prepared-transaction", dsn, table)
	if err != nil {
		return nil, err
	}
	defer closeOnError(ctx, s, &err)

	a, err := s.begin(ctx, "A")
	if err != nil {
		return nil, err
	}
	if err = s.updateRow(ctx, a, 4); err != nil {
		return nil, err
	}
	if _, err = a.conn.Exec(ctx, "PREPARE TRANSACTION "+quoteLiteral(gid)); err != nil {
		return nil, fmt.Errorf("scenario: prepare transaction: %w", err)
	}
	s.prepared = append(s.prepared, gid)
	a.tx = nil // the transaction now lives on without a session

	b, err := s.begin(ctx, "B")
	if err != nil {
		return nil, err
	}
	s.updateRowAsync(b, 4)
	if err = s.WaitBlocked(ctx, b.PID, 0); err != nil {
		return nil, err
	}
	return s, nil
}

func closeOnError(ctx context.Context, s *Scenario, err *error) {
	if *err != nil {
		_ = s.Close(context.WithoutCancel(ctx))
	}
}
