package collector

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/locklens/locklens/internal/observe"
)

const sqlServerFacts = `
SELECT current_setting('server_version'),
       current_setting('server_version_num')::int,
       pg_catalog.pg_is_in_recovery(),
       pg_catalog.pg_has_role('pg_read_all_stats', 'USAGE'),
       current_setting('track_activities')::bool,
       current_setting('track_io_timing')::bool`

// pg_stat_statements must be installed in the database LockLens connects to;
// the view then covers statements of every database of the instance.
const sqlStatementsExtension = `
SELECT n.nspname, e.extversion
FROM pg_catalog.pg_extension AS e
JOIN pg_catalog.pg_namespace AS n ON n.oid = e.extnamespace
WHERE e.extname = 'pg_stat_statements'`

// DetectCapabilities inspects the server inside a read-only transaction.
func DetectCapabilities(ctx context.Context, tx pgx.Tx) (observe.Capabilities, error) {
	return detectCapabilities(ctx, tx)
}

// detectCapabilities inspects the server inside a read-only transaction.
// Probes that may fail (pg_stat_statements not preloaded) run under a
// savepoint so they cannot abort the transaction.
func detectCapabilities(ctx context.Context, tx pgx.Tx) (observe.Capabilities, error) {
	var c observe.Capabilities
	c.DetectedAt = time.Now().UTC()
	if err := tx.QueryRow(ctx, sqlServerFacts).Scan(&c.ServerVersion, &c.VersionNum, &c.IsStandby,
		&c.ReadAllStats, &c.TrackActivities, &c.TrackIOTiming); err != nil {
		return c, err
	}
	c.StatIO = c.VersionNum >= 160000
	c.Checkpointer = c.VersionNum >= 170000

	var schema, version string
	err := tx.QueryRow(ctx, sqlStatementsExtension).Scan(&schema, &version)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		c.StatementsReason = "extension pg_stat_statements is not installed in the database LockLens connects to"
		return c, nil
	case err != nil:
		return c, err
	}
	c.StatementsSchema, c.StatementsVer = schema, version
	probe := "SELECT 1 FROM " + pgx.Identifier{schema, "pg_stat_statements"}.Sanitize() + " LIMIT 1"
	if err := withSavepoint(ctx, tx, func() error { _, err := tx.Exec(ctx, probe); return err }); err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) {
			c.StatementsReason = "pg_stat_statements is installed but not readable: " + pgErr.Message
			return c, nil
		}
		return c, err
	}
	c.StatStatements = true
	return c, nil
}

// withSavepoint runs fn under a savepoint and rolls back to it on error, so
// the surrounding transaction stays usable.
func withSavepoint(ctx context.Context, tx pgx.Tx, fn func() error) error {
	if _, err := tx.Exec(ctx, "SAVEPOINT locklens_probe"); err != nil {
		return err
	}
	if err := fn(); err != nil {
		if _, rbErr := tx.Exec(ctx, "ROLLBACK TO SAVEPOINT locklens_probe"); rbErr != nil {
			return rbErr
		}
		return err
	}
	_, err := tx.Exec(ctx, "RELEASE SAVEPOINT locklens_probe")
	return err
}
