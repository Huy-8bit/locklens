package collector

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/locklens/locklens/internal/domain/observe"
	"github.com/locklens/locklens/internal/repository"
)

// The catalog of the database LockLens connects to, for the query
// optimizer. Only system catalogs and statistics views are read, without
// size functions (relpages estimates instead), so no relation lock is taken.
// Bounded so a database with many relations stays cheap.
const (
	maxCatalogTables  = 2000
	maxCatalogIndexes = 5000
	maxCatalogFKs     = 2000
	maxCatalogColumns = 20000
)

const sqlCatalogTables = `
SELECT c.oid, n.nspname, c.relname, c.reltuples::float8, c.relpages::bigint,
       coalesce(s.seq_scan, 0), coalesce(s.seq_tup_read, 0), coalesce(s.idx_scan, 0),
       coalesce(s.n_tup_ins + s.n_tup_upd + s.n_tup_del, 0), coalesce(s.n_live_tup, 0),
       coalesce(s.n_mod_since_analyze, 0), s.last_analyze, s.last_autoanalyze, c.relispartition
FROM pg_catalog.pg_class AS c
JOIN pg_catalog.pg_namespace AS n ON n.oid = c.relnamespace
LEFT JOIN pg_catalog.pg_stat_user_tables AS s ON s.relid = c.oid
WHERE c.relkind IN ('r', 'p', 'm') AND n.nspname NOT IN ('pg_catalog', 'information_schema') AND n.nspname NOT LIKE 'pg\_toast%'
ORDER BY c.relpages DESC
LIMIT $1`

const sqlCatalogIndexes = `
SELECT x.indexrelid, x.indrelid, n.nspname, t.relname, i.relname, am.amname,
       ARRAY(SELECT coalesce(a.attname, '') FROM unnest(x.indkey::int2[]) WITH ORDINALITY AS k(attnum, ord)
             LEFT JOIN pg_catalog.pg_attribute AS a ON a.attrelid = x.indrelid AND a.attnum = k.attnum AND k.attnum > 0
             ORDER BY k.ord),
       x.indnkeyatts, x.indisunique, x.indisprimary, x.indisvalid, x.indpred IS NOT NULL, x.indexprs IS NOT NULL,
       EXISTS (SELECT 1 FROM pg_catalog.pg_constraint AS k WHERE k.conindid = x.indexrelid),
       pg_catalog.pg_get_indexdef(x.indexrelid), i.relpages::bigint * current_setting('block_size')::bigint, s.idx_scan
FROM pg_catalog.pg_index AS x
JOIN pg_catalog.pg_class AS i ON i.oid = x.indexrelid
JOIN pg_catalog.pg_class AS t ON t.oid = x.indrelid
JOIN pg_catalog.pg_namespace AS n ON n.oid = t.relnamespace
JOIN pg_catalog.pg_am AS am ON am.oid = i.relam
LEFT JOIN pg_catalog.pg_stat_user_indexes AS s ON s.indexrelid = x.indexrelid
WHERE n.nspname NOT IN ('pg_catalog', 'information_schema') AND n.nspname NOT LIKE 'pg\_toast%'
LIMIT $1`

const sqlCatalogFKs = `
SELECT k.conname, n.nspname, t.relname,
       ARRAY(SELECT a.attname FROM unnest(k.conkey) WITH ORDINALITY AS c(attnum, ord)
             JOIN pg_catalog.pg_attribute AS a ON a.attrelid = k.conrelid AND a.attnum = c.attnum ORDER BY c.ord),
       rn.nspname, r.relname, k.confdeltype::text, k.conrelid
FROM pg_catalog.pg_constraint AS k
JOIN pg_catalog.pg_class AS t ON t.oid = k.conrelid
JOIN pg_catalog.pg_namespace AS n ON n.oid = t.relnamespace
JOIN pg_catalog.pg_class AS r ON r.oid = k.confrelid
JOIN pg_catalog.pg_namespace AS rn ON rn.oid = r.relnamespace
WHERE k.contype = 'f' AND n.nspname NOT IN ('pg_catalog', 'information_schema')
LIMIT $1`

// pg_stats shows only columns the role may read; most_common_vals and
// histogram_bounds (data values) are never selected.
const sqlCatalogColumns = `
SELECT schemaname, tablename, attname, null_frac::float8, n_distinct::float8, correlation::float8
FROM pg_catalog.pg_stats
WHERE schemaname NOT IN ('pg_catalog', 'information_schema') AND schemaname NOT LIKE 'pg\_toast%'
LIMIT $1`

func readCatalog(ctx context.Context, tx pgx.Tx) (observe.Catalog, error) {
	c := observe.Catalog{At: time.Now().UTC(), Warnings: []string{}, Extensions: map[string]string{}, Settings: map[string]string{},
		Tables: []observe.CatalogTable{}, Indexes: []observe.CatalogIndex{}, ForeignKeys: []observe.CatalogFK{}, Columns: []observe.ColumnStats{}}
	if err := tx.QueryRow(ctx, `SELECT current_database(), (SELECT stats_reset FROM pg_catalog.pg_stat_database WHERE datname = current_database()), pg_catalog.pg_is_in_recovery()`).
		Scan(&c.DatName, &c.StatsReset, &c.InRecovery); err != nil {
		return c, err
	}
	rows, err := tx.Query(ctx, sqlCatalogTables, maxCatalogTables)
	if err != nil {
		return c, err
	}
	var t observe.CatalogTable
	if _, err := pgx.ForEachRow(rows, []any{&t.RelID, &t.Schema, &t.Name, &t.RelTuples, &t.RelPages, &t.SeqScan, &t.SeqTupRead, &t.IdxScan,
		&t.Writes, &t.LiveTuples, &t.ModSinceAnalyze, &t.LastAnalyze, &t.LastAutoAnalyze, &t.PartitionedChild}, func() error {
		c.Tables = append(c.Tables, t)
		return nil
	}); err != nil {
		return c, err
	}
	if len(c.Tables) == maxCatalogTables {
		c.Warnings = append(c.Warnings, "only the largest 2,000 tables are analysed")
	}
	if rows, err = tx.Query(ctx, sqlCatalogIndexes, maxCatalogIndexes); err != nil {
		return c, err
	}
	var ix observe.CatalogIndex
	if _, err := pgx.ForEachRow(rows, []any{&ix.RelID, &ix.TableRelID, &ix.Schema, &ix.Table, &ix.Name, &ix.Method, &ix.Columns, &ix.KeyColumns,
		&ix.Unique, &ix.Primary, &ix.Valid, &ix.Partial, &ix.Expressions, &ix.Constraint, &ix.Definition, &ix.SizeBytes, &ix.Scans}, func() error {
		cp := ix
		cp.Columns = append([]string(nil), ix.Columns...)
		c.Indexes = append(c.Indexes, cp)
		return nil
	}); err != nil {
		return c, err
	}
	if rows, err = tx.Query(ctx, sqlCatalogFKs, maxCatalogFKs); err != nil {
		return c, err
	}
	var fk observe.CatalogFK
	if _, err := pgx.ForEachRow(rows, []any{&fk.Name, &fk.Schema, &fk.Table, &fk.Columns, &fk.RefSchema, &fk.RefTable, &fk.OnDelete, &fk.TableRelID}, func() error {
		cp := fk
		cp.Columns = append([]string(nil), fk.Columns...)
		c.ForeignKeys = append(c.ForeignKeys, cp)
		return nil
	}); err != nil {
		return c, err
	}
	if rows, err = tx.Query(ctx, sqlCatalogColumns, maxCatalogColumns); err != nil {
		return c, err
	}
	var col observe.ColumnStats
	if _, err := pgx.ForEachRow(rows, []any{&col.Schema, &col.Table, &col.Column, &col.NullFrac, &col.NDistinct, &col.Correlation}, func() error {
		c.Columns = append(c.Columns, col)
		return nil
	}); err != nil {
		return c, err
	}
	if len(c.Columns) == 0 {
		c.Warnings = append(c.Warnings, "no column statistics are visible to the monitoring role (pg_stats shows only columns it may read), so index column order uses the statements only")
	}
	if rows, err = tx.Query(ctx, `SELECT extname, extversion FROM pg_catalog.pg_extension`); err != nil {
		return c, err
	}
	var name, version string
	if _, err = pgx.ForEachRow(rows, []any{&name, &version}, func() error {
		c.Extensions[name] = version
		return nil
	}); err != nil {
		return c, err
	}
	if rows, err = tx.Query(ctx, `SELECT name, setting FROM pg_catalog.pg_settings
WHERE name IN ('work_mem', 'default_statistics_target', 'random_page_cost', 'effective_cache_size', 'server_version_num')`); err != nil {
		return c, err
	}
	if _, err = pgx.ForEachRow(rows, []any{&name, &version}, func() error {
		c.Settings[name] = version
		return nil
	}); err != nil {
		return c, err
	}
	_, _ = fmt.Sscan(c.Settings["server_version_num"], &c.VersionNum)
	return c, nil
}

func (w *worker) collectCatalog(ctx context.Context) error {
	var c observe.Catalog
	if err := w.obs.ReadOnly(ctx, func(ctx context.Context, tx pgx.Tx, _ []int32) error {
		var err error
		c, err = readCatalog(ctx, tx)
		return err
	}); err != nil {
		return err
	}
	id := w.target.ID
	w.m.writer.Enqueue(WriteOp{TargetID: id, Collector: CollectorCatalog, At: c.At,
		Write: func(ctx context.Context, s *repository.Store, _ repository.IdentityCache) error {
			return s.SaveCatalog(ctx, id, c)
		}})
	return nil
}
