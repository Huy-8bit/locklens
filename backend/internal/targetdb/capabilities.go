package targetdb

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/locklens/locklens/internal/domain/observe"
	"github.com/locklens/locklens/internal/targetdb/queries"
)

// DetectCapabilities inspects the server inside a read-only transaction.
// Probes that may fail (pg_stat_statements not preloaded) run under a
// savepoint so they cannot abort the transaction.
func DetectCapabilities(ctx context.Context, tx pgx.Tx) (observe.Capabilities, error) {
	var c observe.Capabilities
	c.DetectedAt = time.Now().UTC()
	if err := tx.QueryRow(ctx, queries.ServerFacts).Scan(&c.ServerVersion, &c.VersionNum, &c.IsStandby,
		&c.ReadAllStats, &c.TrackActivities, &c.TrackIOTiming); err != nil {
		return c, err
	}
	c.StatIO = c.VersionNum >= 160000
	c.Checkpointer = c.VersionNum >= 170000

	var schema, version string
	err := tx.QueryRow(ctx, queries.StatementsExtension).Scan(&schema, &version)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		c.StatementsReason = "extension pg_stat_statements is not installed in the database LockLens connects to"
		return c, nil
	case err != nil:
		return c, err
	}
	c.StatementsSchema, c.StatementsVer = schema, version
	probe := queries.StatementsProbe(pgx.Identifier{schema, "pg_stat_statements"}.Sanitize())
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
	if _, err := tx.Exec(ctx, queries.Savepoint); err != nil {
		return err
	}
	if err := fn(); err != nil {
		if _, rbErr := tx.Exec(ctx, queries.RollbackToSavepoint); rbErr != nil {
			return rbErr
		}
		return err
	}
	_, err := tx.Exec(ctx, queries.ReleaseSavepoint)
	return err
}
