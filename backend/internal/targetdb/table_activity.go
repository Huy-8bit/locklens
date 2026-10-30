package targetdb

import (
	"context"

	"github.com/jackc/pgx/v5"

	"github.com/locklens/locklens/internal/domain/observe"
	"github.com/locklens/locklens/internal/targetdb/queries"
)

// ReadTableCounters reads the access counters of every user table of the
// connected database, by relation OID.
func ReadTableCounters(ctx context.Context, tx pgx.Tx) (map[uint32]observe.TableCounters, error) {
	rows, err := tx.Query(ctx, queries.TableCounters)
	if err != nil {
		return nil, err
	}
	out := map[uint32]observe.TableCounters{}
	var relid uint32
	var c observe.TableCounters
	_, err = pgx.ForEachRow(rows, []any{&relid, &c.Schema, &c.Name, &c.SeqScan, &c.SeqRead, &c.IdxScan, &c.IdxFetch, &c.Writes, &c.Dead, &c.Live}, func() error {
		out[relid] = c
		return nil
	})
	return out, err
}
