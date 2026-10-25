package collector

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/locklens/locklens/internal/domain/observe"
	"github.com/locklens/locklens/internal/repository"
)

// Table statistics of the database LockLens connects to (pg_stat_user_tables
// is per database). Limited to the 200 largest tables. Size functions take an
// ACCESS SHARE lock per relation; the collector runs them under a short
// lock_timeout and falls back to relpages-based estimates if a relation is
// locked exclusively.
const sqlTableStatsExact = `
SELECT t.relid, current_database(), t.schemaname, t.relname, t.n_live_tup, t.n_dead_tup,
       t.n_mod_since_analyze, t.last_vacuum, t.last_autovacuum, t.last_analyze, t.last_autoanalyze,
       t.vacuum_count, t.autovacuum_count, t.analyze_count, t.autoanalyze_count,
       pg_catalog.pg_table_size(t.relid), pg_catalog.pg_indexes_size(t.relid),
       pg_catalog.pg_total_relation_size(t.relid), pg_catalog.age(c.relfrozenxid)::bigint
FROM pg_catalog.pg_stat_user_tables AS t
JOIN pg_catalog.pg_class AS c ON c.oid = t.relid
ORDER BY pg_catalog.pg_total_relation_size(t.relid) DESC NULLS LAST
LIMIT 200`

const sqlTableStatsEstimate = `
SELECT t.relid, current_database(), t.schemaname, t.relname, t.n_live_tup, t.n_dead_tup,
       t.n_mod_since_analyze, t.last_vacuum, t.last_autovacuum, t.last_analyze, t.last_autoanalyze,
       t.vacuum_count, t.autovacuum_count, t.analyze_count, t.autoanalyze_count,
       c.relpages::bigint * current_setting('block_size')::bigint, NULL::bigint, NULL::bigint,
       pg_catalog.age(c.relfrozenxid)::bigint
FROM pg_catalog.pg_stat_user_tables AS t
JOIN pg_catalog.pg_class AS c ON c.oid = t.relid
ORDER BY c.relpages DESC
LIMIT 200`

// Index usage. idx_scan counts only scans since the last statistics reset
// and says nothing about constraint enforcement, so the UI never suggests
// dropping an index based on it.
const sqlIndexStatsExact = `
SELECT i.indexrelid, i.relid, i.schemaname, i.relname, i.indexrelname, i.idx_scan,
       pg_catalog.pg_relation_size(i.indexrelid), x.indisunique, x.indisprimary,
       EXISTS (SELECT 1 FROM pg_catalog.pg_constraint AS k WHERE k.conindid = i.indexrelid)
FROM pg_catalog.pg_stat_user_indexes AS i
JOIN pg_catalog.pg_index AS x ON x.indexrelid = i.indexrelid
ORDER BY pg_catalog.pg_relation_size(i.indexrelid) DESC
LIMIT 300`

const sqlIndexStatsEstimate = `
SELECT i.indexrelid, i.relid, i.schemaname, i.relname, i.indexrelname, i.idx_scan,
       c.relpages::bigint * current_setting('block_size')::bigint, x.indisunique, x.indisprimary,
       EXISTS (SELECT 1 FROM pg_catalog.pg_constraint AS k WHERE k.conindid = i.indexrelid)
FROM pg_catalog.pg_stat_user_indexes AS i
JOIN pg_catalog.pg_index AS x ON x.indexrelid = i.indexrelid
JOIN pg_catalog.pg_class AS c ON c.oid = i.indexrelid
ORDER BY c.relpages DESC
LIMIT 300`

// Database sizes need CONNECT on the database or pg_read_all_stats; others
// are reported as unknown instead of failing the whole query.
const sqlDatabaseAges = `
SELECT d.datname, pg_catalog.age(d.datfrozenxid)::bigint, pg_catalog.mxid_age(d.datminmxid)::bigint,
       CASE WHEN pg_catalog.has_database_privilege(d.datname, 'CONNECT')
              OR pg_catalog.pg_has_role('pg_read_all_stats', 'USAGE')
            THEN pg_catalog.pg_database_size(d.datname) END
FROM pg_catalog.pg_database AS d
WHERE NOT d.datistemplate
ORDER BY d.datname`

const sqlVacuumSettings = `
SELECT name, setting FROM pg_catalog.pg_settings
WHERE name IN ('autovacuum', 'autovacuum_freeze_max_age', 'autovacuum_multixact_freeze_max_age',
               'autovacuum_vacuum_scale_factor', 'autovacuum_vacuum_threshold',
               'autovacuum_analyze_scale_factor', 'autovacuum_naptime', 'autovacuum_max_workers',
               'vacuum_freeze_min_age', 'vacuum_freeze_table_age', 'track_counts')`

func readTableHealth(ctx context.Context, tx pgx.Tx) (observe.TableHealth, error) {
	h := observe.TableHealth{At: time.Now().UTC(), SizeSource: "exact", Settings: map[string]string{}, Warnings: []string{}}
	if err := tx.QueryRow(ctx, "SELECT current_database()").Scan(&h.DatName); err != nil {
		return h, err
	}
	if _, err := tx.Exec(ctx, "SET LOCAL lock_timeout = '250ms'"); err != nil {
		return h, err
	}
	var err error
	err = withSavepoint(ctx, tx, func() error {
		h.Tables, err = scanTables(ctx, tx, sqlTableStatsExact)
		if err != nil {
			return err
		}
		h.Indexes, err = scanIndexes(ctx, tx, sqlIndexStatsExact)
		return err
	})
	if isLockTimeout(err) {
		h.SizeSource = "estimate"
		h.Warnings = append(h.Warnings, "a relation was locked exclusively; sizes are estimates from pg_class.relpages (updated by VACUUM and ANALYZE)")
		if h.Tables, err = scanTables(ctx, tx, sqlTableStatsEstimate); err != nil {
			return h, err
		}
		if h.Indexes, err = scanIndexes(ctx, tx, sqlIndexStatsEstimate); err != nil {
			return h, err
		}
	} else if err != nil {
		return h, err
	}

	rows, err := tx.Query(ctx, sqlDatabaseAges)
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
	rows, err = tx.Query(ctx, sqlVacuumSettings)
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

func (w *worker) collectTables(ctx context.Context) error {
	var h observe.TableHealth
	if err := w.obs.ReadOnly(ctx, func(ctx context.Context, tx pgx.Tx, _ []int32) error {
		var err error
		h, err = readTableHealth(ctx, tx)
		return err
	}); err != nil {
		return err
	}
	var pts []observe.Point
	var total float64
	known := true
	for _, d := range h.Databases {
		var size *float64
		if d.SizeBytes != nil {
			size = observe.F(float64(*d.SizeBytes))
			total += *size
		} else {
			known = false
		}
		var age *float64
		if d.XIDAge != nil {
			age = observe.F(float64(*d.XIDAge))
		}
		pts = append(pts, observe.Point{Metric: "db.size_bytes", Label: d.DatName, Value: size},
			observe.Point{Metric: "db.xid_age", Label: d.DatName, Value: age})
	}
	if known && len(h.Databases) > 0 {
		pts = append(pts, observe.Point{Metric: "db.total_size_bytes", Value: observe.F(total)})
	} else {
		pts = append(pts, observe.Point{Metric: "db.total_size_bytes"})
	}
	w.state.mu.Lock()
	w.state.tables = &h
	w.state.mu.Unlock()
	w.state.addPoints(h.At, pts)
	id, at := w.target.ID, h.At
	w.m.writer.Enqueue(WriteOp{TargetID: id, Collector: CollectorTables, At: at,
		Write: func(ctx context.Context, s *repository.Store, _ repository.IdentityCache) error {
			if err := s.InsertTableHealth(ctx, id, h); err != nil {
				return err
			}
			return s.InsertPoints(ctx, id, at, pts)
		}})
	return nil
}
