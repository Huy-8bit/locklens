package targetdb

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/locklens/locklens/internal/domain/observe"
	"github.com/locklens/locklens/internal/targetdb/queries"
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

func ReadCatalog(ctx context.Context, tx pgx.Tx) (observe.Catalog, error) {
	c := observe.Catalog{At: time.Now().UTC(), Warnings: []string{}, Extensions: map[string]string{}, Settings: map[string]string{},
		Tables: []observe.CatalogTable{}, Indexes: []observe.CatalogIndex{}, ForeignKeys: []observe.CatalogFK{}, Columns: []observe.ColumnStats{}}
	if err := tx.QueryRow(ctx, queries.CatalogDatabase).
		Scan(&c.DatName, &c.StatsReset, &c.InRecovery); err != nil {
		return c, err
	}
	rows, err := tx.Query(ctx, queries.CatalogTables, maxCatalogTables)
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
		c.Truncated = true
	}
	if rows, err = tx.Query(ctx, queries.CatalogIndexes, maxCatalogIndexes); err != nil {
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
	if len(c.Indexes) == maxCatalogIndexes {
		c.Truncated = true
	}
	if rows, err = tx.Query(ctx, queries.CatalogFKs, maxCatalogFKs); err != nil {
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
	if rows, err = tx.Query(ctx, queries.CatalogColumns, maxCatalogColumns); err != nil {
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
	if rows, err = tx.Query(ctx, queries.CatalogExtensions); err != nil {
		return c, err
	}
	var name, version string
	if _, err = pgx.ForEachRow(rows, []any{&name, &version}, func() error {
		c.Extensions[name] = version
		return nil
	}); err != nil {
		return c, err
	}
	if rows, err = tx.Query(ctx, queries.CatalogSettings); err != nil {
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
