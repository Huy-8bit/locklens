package targetdb

import (
	"context"

	"github.com/jackc/pgx/v5"

	"github.com/locklens/locklens/internal/targetdb/queries"
)

// ListDatabases returns the databases the role may connect to, the connected
// one first, at most limit.
func ListDatabases(ctx context.Context, tx pgx.Tx, limit int) ([]string, error) {
	rows, err := tx.Query(ctx, queries.ConnectableDatabases, limit)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[string])
}
