package investigate

import (
	"context"

	"github.com/jackc/pgx/v5"

	"github.com/locklens/locklens/internal/targetdb/queries"
)

// UnsafeRole reports whether the connected role is administrative or can
// read or write server files (directly or through a role it belongs to).
func UnsafeRole(ctx context.Context, conn *pgx.Conn) (bool, error) {
	var unsafe bool
	err := conn.QueryRow(ctx, queries.UnsafeRole).Scan(&unsafe)
	return unsafe, err
}
