// Package repository is the data access layer of the LockLens monitoring store: a dedicated PostgreSQL
// database holding the admin account, sessions, targets, collected history,
// findings, and incidents.
//
// The store is never the database being monitored. It uses its own pool and
// credentials; target connections are managed by package collector.
package repository

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/locklens/locklens/internal/config"
	"github.com/locklens/locklens/migrations"
)

// MinStoreVersionNum is the oldest PostgreSQL release supported as the store
// (date_bin, used for downsampling, exists since 14).
const MinStoreVersionNum = 140000

// migrationLockKey is the advisory lock that serialises concurrent migrators.
const migrationLockKey = 7340012411

// ErrNotFound is returned when a requested row does not exist.
var ErrNotFound = errors.New("not found")

// Store wraps the connection pool of the monitoring store.
type Store struct {
	pool *pgxpool.Pool
	red  *config.Redactor
	// timescale is set once by SetupTimescale, before the store is shared.
	timescale TimescaleStatus
}

// Options configure the store pool.
type Options struct {
	MaxConns       int32
	ConnectTimeout time.Duration
}

// Open connects to the store. It does not run migrations.
func Open(ctx context.Context, dsn string, opts Options) (*Store, error) {
	red := config.NewRedactor(dsn)
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, &config.InvalidDSNError{}
	}
	red.Add(cfg.ConnConfig.Password)
	if opts.MaxConns > 0 {
		cfg.MaxConns = opts.MaxConns
	} else {
		cfg.MaxConns = 8
	}
	if opts.ConnectTimeout > 0 {
		cfg.ConnConfig.ConnectTimeout = opts.ConnectTimeout
	}
	cfg.ConnConfig.RuntimeParams["application_name"] = "locklens-store"
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, red.Error(err)
	}
	s := &Store{pool: pool, red: red}
	if err := s.Ping(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	var num int
	if err := pool.QueryRow(ctx, "SELECT current_setting('server_version_num')::int").Scan(&num); err != nil {
		pool.Close()
		return nil, red.Error(err)
	}
	if num < MinStoreVersionNum {
		pool.Close()
		return nil, fmt.Errorf("monitoring store runs PostgreSQL %d; 14 or later is required", num/10000)
	}
	return s, nil
}

// Close releases the pool.
func (s *Store) Close() { s.pool.Close() }

// Pool exposes the pool for packages that need transactions.
func (s *Store) Pool() *pgxpool.Pool { return s.pool }

// Ping checks that the store answers.
func (s *Store) Ping(ctx context.Context) error {
	if err := s.pool.Ping(ctx); err != nil {
		return s.red.Error(err)
	}
	return nil
}

// Redact removes store credentials from an error message.
func (s *Store) Redact(err error) error { return s.red.Error(err) }

// Migration is one versioned schema file.
type Migration struct {
	Version int
	Name    string
	SQL     string
}

// LoadMigrations returns the embedded migrations in version order.
func LoadMigrations() ([]Migration, error) {
	entries, err := fs.ReadDir(migrations.FS, ".")
	if err != nil {
		return nil, err
	}
	var out []Migration
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".sql") {
			continue
		}
		num, _, ok := strings.Cut(name, "_")
		v, err := strconv.Atoi(num)
		if !ok || err != nil {
			return nil, fmt.Errorf("migration %s: name must start with a number and an underscore", name)
		}
		body, err := fs.ReadFile(migrations.FS, name)
		if err != nil {
			return nil, err
		}
		out = append(out, Migration{Version: v, Name: name, SQL: string(body)})
	}
	slices.SortFunc(out, func(a, b Migration) int { return a.Version - b.Version })
	for i := 1; i < len(out); i++ {
		if out[i].Version == out[i-1].Version {
			return nil, fmt.Errorf("duplicate migration version %d", out[i].Version)
		}
	}
	return out, nil
}

// Migrate applies pending migrations. A session-level advisory lock makes
// concurrent starts safe; each migration runs in its own transaction together
// with its schema_migrations row, so a failed migration leaves no trace.
// It returns the versions that were applied.
func (s *Store) Migrate(ctx context.Context) ([]int, error) {
	all, err := LoadMigrations()
	if err != nil {
		return nil, err
	}
	conn, err := s.pool.Acquire(ctx)
	if err != nil {
		return nil, s.red.Error(err)
	}
	defer conn.Release()
	if _, err := conn.Exec(ctx, "SELECT pg_advisory_lock($1)", migrationLockKey); err != nil {
		return nil, s.red.Error(err)
	}
	defer conn.Exec(context.WithoutCancel(ctx), "SELECT pg_advisory_unlock($1)", migrationLockKey) //nolint:errcheck

	if _, err := conn.Exec(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		version    integer PRIMARY KEY,
		name       text        NOT NULL,
		applied_at timestamptz NOT NULL DEFAULT now())`); err != nil {
		return nil, s.red.Error(err)
	}
	applied := map[int]bool{}
	rows, err := conn.Query(ctx, "SELECT version FROM schema_migrations")
	if err != nil {
		return nil, s.red.Error(err)
	}
	versions, err := pgx.CollectRows(rows, pgx.RowTo[int])
	if err != nil {
		return nil, s.red.Error(err)
	}
	for _, v := range versions {
		applied[v] = true
	}
	latest := 0
	if len(all) > 0 {
		latest = all[len(all)-1].Version
	}
	for _, v := range versions {
		if v > latest {
			return nil, fmt.Errorf("monitoring store has schema version %d, newer than this LockLens build supports (%d)", v, latest)
		}
	}

	var done []int
	for _, m := range all {
		if applied[m.Version] {
			continue
		}
		err := pgx.BeginFunc(ctx, conn, func(tx pgx.Tx) error {
			if _, err := tx.Exec(ctx, m.SQL); err != nil {
				return err
			}
			_, err := tx.Exec(ctx, "INSERT INTO schema_migrations (version, name) VALUES ($1, $2)", m.Version, m.Name)
			return err
		})
		if err != nil {
			return done, fmt.Errorf("apply migration %s: %w", m.Name, s.red.Error(err))
		}
		done = append(done, m.Version)
	}
	return done, nil
}

// SchemaVersion returns the highest applied migration, or 0.
func (s *Store) SchemaVersion(ctx context.Context) (int, error) {
	var v *int
	err := s.pool.QueryRow(ctx, "SELECT max(version) FROM schema_migrations").Scan(&v)
	if err != nil {
		return 0, s.red.Error(err)
	}
	if v == nil {
		return 0, nil
	}
	return *v, nil
}
