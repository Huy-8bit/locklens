package targetdb

import (
	"context"

	"github.com/jackc/pgx/v5"

	"github.com/locklens/locklens/internal/domain/observe"
	"github.com/locklens/locklens/internal/targetdb/queries"
)

// ReadLoadSample reads the sessions that are load now, except own.
func ReadLoadSample(ctx context.Context, tx pgx.Tx, own []int32) ([]observe.LoadSession, error) {
	rows, err := tx.Query(ctx, queries.LoadSample, own)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (observe.LoadSession, error) {
		var s observe.LoadSession
		var dbid, userid int64
		err := r.Scan(&s.State, &s.WaitEventType, &s.WaitEvent, &s.BackendType, &dbid, &s.DatName, &userid, &s.UserName, &s.Application, &s.QueryID)
		s.DBID, s.UserID = uint32(dbid), uint32(userid)
		return s, err
	})
}
