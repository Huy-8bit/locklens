package targetdb

import (
	"context"
	"errors"
	"net"
	"strings"
	"syscall"

	"github.com/jackc/pgx/v5/pgconn"
)

func sqlState(err error) string {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code
	}
	return ""
}

// Hint returns a short, actionable suggestion for common connection and
// permission failures, or "" when there is nothing useful to add. Hints never
// repeat connection-string contents.
func Hint(err error) string {
	if err == nil {
		return ""
	}
	switch sqlState(err) {
	case "28P01", "28000":
		return "authentication failed: check the user and password (PGPASSWORD and ~/.pgpass are also consulted) and pg_hba.conf"
	case "3D000":
		return "the database does not exist: check the database name in the DSN"
	case "42501":
		return "permission denied: see docs/postgresql-observation.md for the privileges LockLens needs"
	case "57014":
		return "a query was cancelled by statement_timeout: raise --query-timeout or check server load"
	case "55P03":
		return "LockLens could not obtain a catalog lock within lock_timeout; something holds a strong lock on a system view"
	case "53300":
		return "the server has no free connection slots (max_connections reached)"
	}
	var uv *UnsupportedVersionError
	if errors.As(err, &uv) {
		return "upgrade the server or use an older tool; versions below 14 lack pg_locks.waitstart"
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "the operation timed out: check network reachability, --connect-timeout, and --query-timeout"
	}
	if errors.Is(err, syscall.ECONNREFUSED) {
		return "connection refused: is PostgreSQL running and listening on that host and port?"
	}
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return "the host name could not be resolved"
	}
	msg := err.Error()
	switch {
	case strings.Contains(msg, "server refused TLS connection"), strings.Contains(msg, "tls:"), strings.Contains(msg, "x509:"):
		return "TLS negotiation failed: check sslmode, sslrootcert, and the server certificate"
	case strings.Contains(msg, "no pg_hba.conf entry"):
		return "pg_hba.conf rejects this client: ask the DBA to allow the host, user, database, and SSL mode"
	}
	return ""
}
