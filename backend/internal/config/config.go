// Package config holds LockLens' configuration: the server's settings from the
// environment (server.go) and PostgreSQL connection settings that keep
// credentials out of user-visible output (config.go, redact.go).
package config

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// EnvDSN is the environment variable consulted when --dsn is not given.
const EnvDSN = "LOCKLENS_DSN"

// ApplicationName is set on every LockLens connection so operators can
// recognise them in pg_stat_activity.
const ApplicationName = "locklens"

// ErrNoDSN is returned when neither --dsn nor LOCKLENS_DSN is set.
var ErrNoDSN = errors.New("no connection string: pass --dsn or set " + EnvDSN)

// InvalidDSNError reports a connection string that could not be parsed.
// Its message never contains the connection string or any part of it,
// because a malformed string cannot be redacted reliably.
type InvalidDSNError struct {
	hint string
}

func (e *InvalidDSNError) Error() string {
	msg := "invalid connection string (the value is not shown to protect credentials)"
	if e.hint != "" {
		msg += ": " + e.hint
	}
	return msg
}

// ResolveDSN applies the documented precedence: the --dsn flag first, then
// the LOCKLENS_DSN environment variable.
func ResolveDSN(flagValue string) (string, error) {
	if strings.TrimSpace(flagValue) != "" {
		return flagValue, nil
	}
	if v := os.Getenv(EnvDSN); strings.TrimSpace(v) != "" {
		return v, nil
	}
	return "", ErrNoDSN
}

// Options control the connection pool.
type Options struct {
	ConnectTimeout time.Duration
	// QueryTimeout is the upper bound for a single collection. It is enforced
	// by context and, as a server-side backstop, by statement_timeout.
	QueryTimeout time.Duration
}

// Target is the non-secret description of where LockLens connects.
type Target struct {
	Host     string
	Port     uint16
	Database string
	User     string
}

func (t Target) String() string {
	host := t.Host
	if strings.HasPrefix(host, "/") {
		host = "socket " + host
	}
	return fmt.Sprintf("%s:%d/%s as %s", host, t.Port, t.Database, t.User)
}

// PoolConfig parses dsn and returns a pool configuration tuned for a
// read-only observer, together with a Redactor that knows the secrets found
// in the configuration.
//
// TLS settings are left exactly as pgx derives them from the DSN and the
// standard PG* environment variables; LockLens never weakens sslmode.
func PoolConfig(dsn string, opts Options) (*pgxpool.Config, *Redactor, error) {
	red := NewRedactor(dsn)
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, red, &InvalidDSNError{hint: parseHint(err)}
	}
	// The password may also come from PGPASSWORD or a passfile.
	red.Add(cfg.ConnConfig.Password)

	// One observer connection is enough: collections never overlap. A second
	// slot lets the pool replace a broken connection without waiting.
	cfg.MaxConns = 2
	cfg.MinConns = 0
	cfg.MaxConnIdleTime = time.Minute
	if opts.ConnectTimeout > 0 {
		cfg.ConnConfig.ConnectTimeout = opts.ConnectTimeout
	}

	rp := cfg.ConnConfig.RuntimeParams
	rp["application_name"] = ApplicationName
	// LockLens only observes. Read-only transactions make that a server-side
	// guarantee rather than a promise of this code.
	rp["default_transaction_read_only"] = "on"
	if opts.QueryTimeout > 0 {
		rp["statement_timeout"] = strconv.FormatInt(opts.QueryTimeout.Milliseconds(), 10)
	}
	// The catalog views LockLens reads only need AccessShareLock; if anything
	// ever conflicts with that, fail fast instead of joining the queue we are
	// trying to observe.
	rp["lock_timeout"] = "2000"
	rp["idle_in_transaction_session_timeout"] = "60000"
	return cfg, red, nil
}

// TargetOf returns the non-secret connection target.
func TargetOf(cfg *pgxpool.Config) Target {
	return Target{
		Host:     cfg.ConnConfig.Host,
		Port:     cfg.ConnConfig.Port,
		Database: cfg.ConnConfig.Database,
		User:     cfg.ConnConfig.User,
	}
}

// parseHint gives a coarse, secret-free reason for a parse failure.
func parseHint(err error) string {
	var numErr *strconv.NumError
	var urlErr *url.Error
	switch {
	case errors.As(err, &numErr):
		return "a numeric parameter (such as port or connect_timeout) is not a valid number"
	case errors.As(err, &urlErr):
		return "the postgres:// URL is malformed (special characters in the password must be percent-encoded)"
	}
	var pce *pgconn.ParseConfigError
	if errors.As(err, &pce) {
		return "expected a postgres:// URL or space-separated key=value pairs"
	}
	return ""
}
