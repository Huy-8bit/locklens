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

// ReadTableHealth reads the statistics of the largest tables and indexes of
// the connected database. With clusterWide it also reads every database's
// transaction ID age and size and the vacuum settings (once per instance).
func ReadTableHealth(ctx context.Context, tx pgx.Tx, clusterWide bool) (observe.TableHealth, error) {
	h := observe.TableHealth{At: time.Now().UTC(), SizeSource: "exact", Settings: map[string]string{}, Warnings: []string{}}
	if err := tx.QueryRow(ctx, queries.CurrentDatabase).Scan(&h.DatName); err != nil {
		return h, err
	}
	if _, err := tx.Exec(ctx, queries.ShortLockTimeout); err != nil {
		return h, err
	}
	var err error
	err = withSavepoint(ctx, tx, func() error {
		h.Tables, err = scanTables(ctx, tx, queries.TableStatsExact)
		if err != nil {
			return err
		}
		h.Indexes, err = scanIndexes(ctx, tx, queries.IndexStatsExact)
		return err
	})
	if isLockTimeout(err) {
		h.SizeSource = "estimate"
		h.Warnings = append(h.Warnings, "a relation was locked exclusively; sizes are estimates from pg_class.relpages (updated by VACUUM and ANALYZE)")
		if h.Tables, err = scanTables(ctx, tx, queries.TableStatsEstimate); err != nil {
			return h, err
		}
		if h.Indexes, err = scanIndexes(ctx, tx, queries.IndexStatsEstimate); err != nil {
			return h, err
		}
	} else if err != nil {
		return h, err
	}
	for i := range h.Indexes {
		h.Indexes[i].DatName = h.DatName
	}
	if !clusterWide {
		return h, nil
	}

	rows, err := tx.Query(ctx, queries.DatabaseAges)
	if err != nil {
		return h, err
	}
	h.Databases, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (observe.DatabaseAge, error) {
		var d observe.DatabaseAge
		err := r.Scan(&d.DatName, &d.XIDAge, &d.MXIDAge, &d.SizeBytes)
		return d, err
	})
	if err != nil {
		return h, err
	}
	rows, err = tx.Query(ctx, queries.VacuumSettings)
	if err != nil {
		return h, err
	}
	var name, setting string
	_, err = pgx.ForEachRow(rows, []any{&name, &setting}, func() error {
		h.Settings[name] = setting
		return nil
	})
	return h, err
}

func scanTables(ctx context.Context, tx pgx.Tx, sql string) ([]observe.TableStat, error) {
	rows, err := tx.Query(ctx, sql)
	if err != nil {
		return nil, err
	}
	out, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (observe.TableStat, error) {
		var t observe.TableStat
		err := r.Scan(&t.RelID, &t.DatName, &t.Schema, &t.Name, &t.LiveTuples, &t.DeadTuples, &t.ModSinceAnalyze,
			&t.LastVacuum, &t.LastAutovacuum, &t.LastAnalyze, &t.LastAutoanalyze, &t.VacuumCount, &t.AutovacuumCount,
			&t.AnalyzeCount, &t.AutoanalyzeCount, &t.TableBytes, &t.IndexesBytes, &t.TotalBytes, &t.FrozenXIDAge)
		return t, err
	})
	if out == nil {
		out = []observe.TableStat{}
	}
	return out, err
}

func scanIndexes(ctx context.Context, tx pgx.Tx, sql string) ([]observe.IndexStat, error) {
	rows, err := tx.Query(ctx, sql)
	if err != nil {
		return nil, err
	}
	out, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (observe.IndexStat, error) {
		var i observe.IndexStat
		err := r.Scan(&i.IndexRelID, &i.RelID, &i.Schema, &i.Table, &i.Name, &i.Scans, &i.Bytes,
			&i.IsUnique, &i.IsPrimary, &i.IsConstraint)
		return i, err
	})
	if out == nil {
		out = []observe.IndexStat{}
	}
	return out, err
}

func isLockTimeout(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "55P03"
}
