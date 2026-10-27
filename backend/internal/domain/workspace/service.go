// Package workspace provides opt-in, bounded interactive database access. It
// never borrows monitoring credentials and never persists query text or results.
package workspace

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/locklens/locklens/internal/lib/secret"
	"github.com/locklens/locklens/internal/repository"
)

const MaxRows = 500
const MaxBytes = 1 << 20
const MaxCell = 8192
const declaration = "DECLARE locklens_result NO SCROLL CURSOR FOR "

type Error struct {
	Code     string `json:"code"`
	Message  string `json:"message"`
	Position int32  `json:"position,omitempty"`
}

func (e *Error) Error() string        { return e.Message }
func fail(code, message string) error { return &Error{Code: code, Message: message} }
func SafeError(err error) *Error {
	var e *Error
	if errors.As(err, &e) {
		return e
	}
	var p *pgconn.PgError
	if errors.As(err, &p) {
		pos := p.Position
		if pos > int32(len(declaration)) {
			pos -= int32(len(declaration))
		}
		return &Error{Code: p.Code, Message: "PostgreSQL rejected the operation. Check permissions, statement syntax and execution limits.", Position: pos}
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return &Error{Code: "cancelled", Message: "Operation cancelled or execution deadline reached."}
	}
	return &Error{Code: "database_error", Message: "Database operation failed or exceeded the protocol size limit."}
}

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
	store  *repository.Store
	box    *secret.Box
	mu     sync.Mutex
	active map[string]running
	counts map[int64]int
	total  int
}

func New(store *repository.Store, box *secret.Box) *Service {
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
			return fail("invalid_dsn", "Invalid investigation connection string.")
		}
		// The investigation role may be the monitoring role itself; connect()
		// refuses administrative roles either way.
		if c.Host != t.DisplayHost || int(c.Port) != t.DisplayPort || c.Database != t.DisplayDatabase || !sameFallbacks(c) {
			return fail("invalid_dsn", "Use the target's host, port, and database.")
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
	if (p.Metadata || p.Data || p.SQL) && len(p.Ciphertext) == 0 {
		return fail("credentials_required", "Configure separate investigation credentials first.")
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
func connect(ctx context.Context, c *pgx.ConnConfig) (*pgx.Conn, error) {
	c.ConnectTimeout = 3 * time.Second
	c.DefaultQueryExecMode = pgx.QueryExecModeExec
	c.MaxProtocolMessageBodyLen = MaxBytes
	c.RuntimeParams = map[string]string{"application_name": "locklens-workspace", "default_transaction_read_only": "on", "statement_timeout": "15000", "lock_timeout": "1000", "idle_in_transaction_session_timeout": "20000", "search_path": "pg_catalog,public"}
	c.Tracer = nil
	c.OnNotice = func(*pgconn.PgConn, *pgconn.Notice) {}
	conn, err := pgx.ConnectConfig(ctx, c)
	if err != nil {
		return nil, err
	}
	var unsafe bool
	err = conn.QueryRow(ctx, `SELECT r.rolsuper OR r.rolcreaterole OR r.rolcreatedb OR r.rolbypassrls OR EXISTS(SELECT 1 FROM pg_roles x WHERE pg_has_role(current_user,x.oid,'MEMBER') AND (x.rolsuper OR x.rolcreaterole OR x.rolcreatedb OR x.rolbypassrls OR x.rolname IN ('pg_write_all_data','pg_read_server_files','pg_write_server_files','pg_execute_server_program'))) FROM pg_roles r WHERE r.rolname=current_user`).Scan(&unsafe)
	if err != nil || unsafe {
		_ = conn.Close(context.Background())
		if err != nil {
			return nil, err
		}
		return nil, fail("unsafe_role", "Investigation role must not have administrative or server-file privileges.")
	}
	return conn, nil
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
	allowed := (action == "metadata" && p.Metadata) || (action == "data" && p.Data) || ((action == "sql" || action == "explain") && p.SQL)
	if !allowed {
		return nil, fail("forbidden", "This target policy does not allow this operation.")
	}
	t, err := s.store.GetTarget(ctx, id)
	if err != nil || !t.Enabled || t.DeletedAt != nil {
		return nil, fail("unavailable", "Target is disabled or unavailable.")
	}
	if len(requestID) < 8 || len(requestID) > 80 {
		return nil, fail("invalid_request", "A unique request ID of 8–80 characters is required.")
	}
	ctx, cancel := context.WithTimeout(ctx, 18*time.Second)
	defer cancel()
	s.mu.Lock()
	if s.total >= 8 || s.counts[id] >= 2 {
		s.mu.Unlock()
		return nil, fail("busy", "Interactive connection limit reached (2 per target, 8 globally).")
	}
	if _, exists := s.active[requestID]; exists {
		s.mu.Unlock()
		return nil, fail("busy", "Request ID is already active.")
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
			status = SafeError(err).Code
		}
		rows := 0
		if r, ok := out.(Result); ok {
			rows = len(r.Rows)
		}
		ac, done := context.WithTimeout(context.Background(), 2*time.Second)
		defer done()
		auditErr := s.store.AuditWorkspace(ac, id, actor, action, status, time.Since(started), rows)
		if auditErr != nil && err == nil {
			out = nil
			err = fail("audit_failed", "Operation could not be audited; result withheld.")
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
		return nil, fail("credentials_mismatch", "Reconfigure investigation credentials after changing the target endpoint.")
	}
	current, e := s.store.WorkspacePolicy(ctx, id)
	if e != nil {
		return nil, e
	}
	if !current.UpdatedAt.Equal(p.UpdatedAt) {
		return nil, fail("forbidden", "Policy changed; retry under the current policy.")
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

type Column struct {
	Name string `json:"name"`
	OID  uint32 `json:"type_oid"`
}
type Result struct {
	Columns       []Column    `json:"columns"`
	Rows          [][]*string `json:"rows"`
	Truncated     bool        `json:"truncated"`
	CellTruncated bool        `json:"cell_truncated"`
	Bytes         int         `json:"bytes"`
	DurationMS    int64       `json:"duration_ms"`
	Pagination    string      `json:"pagination,omitempty"`
	Next          *string     `json:"next,omitempty"`
}

func readResult(ctx context.Context, tx pgx.Tx, query string, args ...any) (Result, error) {
	start := time.Now()
	r := Result{Columns: []Column{}, Rows: [][]*string{}}
	rows, err := tx.Query(ctx, query, append([]any{pgx.QueryResultFormats{pgx.TextFormatCode}}, args...)...)
	if err != nil {
		return r, err
	}
	defer rows.Close()
	if len(rows.FieldDescriptions()) > 100 {
		return r, fail("too_many_columns", "Results are limited to 100 columns.")
	}
	for _, f := range rows.FieldDescriptions() {
		r.Columns = append(r.Columns, Column{f.Name, f.DataTypeOID})
	}
	header, _ := json.Marshal(r.Columns)
	r.Bytes = len(header) + 256
	for rows.Next() {
		if len(r.Rows) >= MaxRows {
			r.Truncated = true
			break
		}
		values := rows.RawValues()
		row := make([]*string, len(values))
		for i, v := range values {
			if v != nil {
				if len(v) > MaxCell {
					v = v[:MaxCell]
					for !utf8.Valid(v) {
						v = v[:len(v)-1]
					}
					r.CellTruncated = true
				}
				s := string(v)
				row[i] = &s
			}
		}
		encoded, _ := json.Marshal(row)
		size := len(encoded) + 1
		if r.Bytes+size > MaxBytes-8192 {
			r.Truncated = true
			break
		}
		r.Rows = append(r.Rows, row)
		r.Bytes += size
	}
	if err = rows.Err(); err != nil {
		return r, err
	}
	r.DurationMS = time.Since(start).Milliseconds()
	return r, nil
}
func Execute(ctx context.Context, tx pgx.Tx, sql, schema string, explain bool) (result Result, err error) {
	started := time.Now()
	defer func() { result.DurationMS = time.Since(started).Milliseconds() }()
	if len(sql) == 0 || len(sql) > 32768 {
		return Result{}, fail("invalid_sql", "Statement must contain 1–32768 bytes.")
	}
	if len(schema) > 63 {
		return Result{}, fail("invalid_schema", "Schema name is too long.")
	}
	if schema == "" {
		schema = "public"
	}
	if _, err := tx.Exec(ctx, `SELECT set_config('search_path',$1,true)`, "pg_catalog,"+pgx.Identifier{schema}.Sanitize()); err != nil {
		return Result{}, err
	}
	// Extended protocol accepts exactly one statement. DECLARE uses PostgreSQL's
	// query grammar, not a SELECT-prefix check, and cannot commit the transaction.
	sql = strings.TrimSpace(sql)
	if _, err := tx.Conn().PgConn().ExecParams(ctx, declaration+sql, nil, nil, nil, nil).Close(); err != nil {
		return Result{}, err
	}
	if explain {
		return readResult(ctx, tx, "EXPLAIN (FORMAT JSON) "+sql)
	}
	return readResult(ctx, tx, fmt.Sprintf("FETCH FORWARD %d FROM locklens_result", MaxRows+1))
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

// ExplainGeneric returns the estimated plan of a parameterised statement
// such as a pg_stat_statements text ($1, $2, ...) with EXPLAIN (FORMAT JSON,
// GENERIC_PLAN), PostgreSQL 16 or later. EXPLAIN without ANALYZE plans the
// statement without executing it, and the transaction is read-only. The
// simple query protocol is used because the placeholders have no values;
// a statement containing ';' is refused so only one statement is sent.
func ExplainGeneric(ctx context.Context, tx pgx.Tx, sql string) (string, error) {
	sql = strings.TrimSpace(strings.TrimRight(strings.TrimSpace(sql), ";"))
	if len(sql) == 0 || len(sql) > 32768 {
		return "", fail("invalid_sql", "Statement must contain 1–32768 bytes.")
	}
	if strings.Contains(sql, ";") {
		return "", fail("invalid_sql", "Only a single statement can be explained.")
	}
	results, err := tx.Conn().PgConn().Exec(ctx, "EXPLAIN (FORMAT JSON, GENERIC_PLAN) "+sql).ReadAll()
	if err != nil {
		return "", err
	}
	if len(results) != 1 || len(results[0].Rows) != 1 || len(results[0].Rows[0]) != 1 {
		return "", fail("invalid_result", "EXPLAIN returned an unexpected result.")
	}
	return string(results[0].Rows[0][0]), nil
}
