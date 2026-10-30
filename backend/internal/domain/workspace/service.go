// Package workspace provides opt-in, bounded interactive database access. It
// never borrows monitoring credentials and never persists query text or results.
package workspace

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/locklens/locklens/internal/lib/secret"
	"github.com/locklens/locklens/internal/repository"
	"github.com/locklens/locklens/internal/targetdb/investigate"
)

type actorKey struct{}

// WithActor names the user an operation is audited under.
func WithActor(ctx context.Context, user string) context.Context {
	return context.WithValue(ctx, actorKey{}, user)
}

func actorOf(ctx context.Context) string {
	if a, _ := ctx.Value(actorKey{}).(string); a != "" {
		return a
	}
	return "unknown"
}

type running struct {
	owner  string
	target int64
	cancel context.CancelFunc
}
type Service struct {
	store  Store
	box    *secret.Box
	mu     sync.Mutex
	active map[string]running
	counts map[int64]int
	total  int
}

func New(store Store, box *secret.Box) *Service {
	return &Service{store: store, box: box, active: map[string]running{}, counts: map[int64]int{}}
}
func aad(id int64) []byte { return []byte(fmt.Sprintf("workspace-dsn:%d", id)) }
func (s *Service) Configure(ctx context.Context, id int64, p repository.WorkspacePolicy, dsn string) error {
	t, err := s.store.GetTarget(ctx, id)
	if err != nil {
		return err
	}
	p.TargetID = id
	old, err := s.store.WorkspacePolicy(ctx, id)
	if err != nil {
		return err
	}
	p.Ciphertext = old.Ciphertext
	if dsn != "" {
		c, err := pgx.ParseConfig(dsn)
		if err != nil {
			return investigate.Fail("invalid_dsn", "Invalid investigation connection string.")
		}
		// The investigation role may be the monitoring role itself; connect()
		// refuses administrative roles either way.
		if c.Host != t.DisplayHost || int(c.Port) != t.DisplayPort || c.Database != t.DisplayDatabase || !sameFallbacks(c) {
			return investigate.Fail("invalid_dsn", "Use the target's host, port, and database.")
		}
		// Verify permissions before storing new credentials.
		conn, err := connect(ctx, c)
		if err != nil {
			return err
		}
		_ = conn.Close(context.Background())
		p.Ciphertext, err = s.box.Seal([]byte(dsn), aad(id))
		if err != nil {
			return err
		}
	}
	if p.HypoPG && !p.SQL {
		return investigate.Fail("invalid_policy", "What-if checks with HypoPG run EXPLAIN through the SQL capability; enable SQL first.")
	}
	if (p.Metadata || p.Data || p.SQL) && len(p.Ciphertext) == 0 {
		return investigate.Fail("credentials_required", "Configure separate investigation credentials first.")
	}
	if err = s.store.SetWorkspacePolicy(ctx, p); err != nil {
		return err
	}
	// Policy changes revoke outstanding operations immediately.
	s.mu.Lock()
	for _, r := range s.active {
		if r.target == id {
			r.cancel()
		}
	}
	s.mu.Unlock()
	return nil
}
func (s *Service) Cancel(owner string, target int64, id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.active[id]
	if ok && r.owner == owner && r.target == target {
		r.cancel()
		return true
	}
	return false
}
func (s *Service) Run(ctx context.Context, id int64, owner, action, requestID string, fn func(context.Context, pgx.Tx) (any, error)) (out any, err error) {
	started := time.Now()
	actor := actorOf(ctx)
	p, err := s.store.WorkspacePolicy(ctx, id)
	if err != nil {
		return nil, err
	}
	allowed := (action == "metadata" && p.Metadata) || (action == "data" && p.Data) || ((action == "sql" || action == "explain") && p.SQL) ||
		(action == "whatif" && p.SQL && p.HypoPG)
	if !allowed {
		return nil, investigate.Fail("forbidden", "This target policy does not allow this operation.")
	}
	t, err := s.store.GetTarget(ctx, id)
	if err != nil || !t.Enabled || t.DeletedAt != nil {
		return nil, investigate.Fail("unavailable", "Target is disabled or unavailable.")
	}
	if len(requestID) < 8 || len(requestID) > 80 {
		return nil, investigate.Fail("invalid_request", "A unique request ID of 8–80 characters is required.")
	}
	ctx, cancel := context.WithTimeout(ctx, 18*time.Second)
	defer cancel()
	s.mu.Lock()
	if s.total >= 8 || s.counts[id] >= 2 {
		s.mu.Unlock()
		return nil, investigate.Fail("busy", "Interactive connection limit reached (2 per target, 8 globally).")
	}
	if _, exists := s.active[requestID]; exists {
		s.mu.Unlock()
		return nil, investigate.Fail("busy", "Request ID is already active.")
	}
	s.total++
	s.counts[id]++
	s.active[requestID] = running{owner, id, cancel}
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		s.total--
		s.counts[id]--
		delete(s.active, requestID)
		s.mu.Unlock()
		status := "ok"
		if err != nil {
			status = investigate.SafeError(err).Code
		}
		rows := 0
		if r, ok := out.(investigate.Result); ok {
			rows = len(r.Rows)
		}
		ac, done := context.WithTimeout(context.Background(), 2*time.Second)
		defer done()
		auditErr := s.store.AuditWorkspace(ac, id, actor, action, status, time.Since(started), rows)
		if auditErr != nil && err == nil {
			out = nil
			err = investigate.Fail("audit_failed", "Operation could not be audited; result withheld.")
		}
	}()
	plain, err := s.box.Open(p.Ciphertext, aad(id))
	if err != nil {
		return nil, err
	}
	cfg, err := pgx.ParseConfig(string(plain))
	if err != nil {
		return nil, err
	}
	if cfg.Host != t.DisplayHost || int(cfg.Port) != t.DisplayPort || cfg.Database != t.DisplayDatabase || !sameFallbacks(cfg) {
		return nil, investigate.Fail("credentials_mismatch", "Reconfigure investigation credentials after changing the target endpoint.")
	}
	current, e := s.store.WorkspacePolicy(ctx, id)
	if e != nil {
		return nil, e
	}
	if !current.UpdatedAt.Equal(p.UpdatedAt) {
		return nil, investigate.Fail("forbidden", "Policy changed; retry under the current policy.")
	}
	conn, err := connect(ctx, cfg)
	if err != nil {
		return nil, err
	}
	defer closeConnection(conn)
	tx, err := conn.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, err
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = tx.Rollback(cleanup)
	}()
	return fn(ctx, tx)
}

func connect(ctx context.Context, c *pgx.ConnConfig) (*pgx.Conn, error) {
	c.ConnectTimeout = 3 * time.Second
	c.DefaultQueryExecMode = pgx.QueryExecModeExec
	c.MaxProtocolMessageBodyLen = investigate.MaxBytes
	c.RuntimeParams = map[string]string{"application_name": "locklens-workspace", "default_transaction_read_only": "on", "statement_timeout": "15000", "lock_timeout": "1000", "idle_in_transaction_session_timeout": "20000", "search_path": "pg_catalog,public"}
	c.Tracer = nil
	c.OnNotice = func(*pgconn.PgConn, *pgconn.Notice) {}
	conn, err := pgx.ConnectConfig(ctx, c)
	if err != nil {
		return nil, err
	}
	unsafe, err := investigate.UnsafeRole(ctx, conn)
	if err != nil || unsafe {
		_ = conn.Close(context.Background())
		if err != nil {
			return nil, err
		}
		return nil, investigate.Fail("unsafe_role", "Investigation role must not have administrative or server-file privileges.")
	}
	return conn, nil
}

func sameFallbacks(c *pgx.ConnConfig) bool {
	for _, f := range c.Fallbacks {
		if f.Host != c.Host || f.Port != c.Port {
			return false
		}
	}
	return true
}

func closeConnection(conn *pgx.Conn) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_ = conn.Close(ctx)
}
