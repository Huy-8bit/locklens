package queries

import (
	_ "embed"
	"fmt"
	"strings"
)

// fill replaces the {{name}} placeholders of a template. Values come only
// from the fixed lists of this package, never from a request.
func fill(tmpl string, kv ...string) string {
	pairs := make([]string, 0, len(kv))
	for i := 0; i+1 < len(kv); i += 2 {
		pairs = append(pairs, "{{"+kv[i]+"}}", kv[i+1])
	}
	return strings.NewReplacer(pairs...).Replace(tmpl)
}

// quoteIdent quotes a table name of the fixed lists below.
func quoteIdent(s string) string { return `"` + strings.ReplaceAll(s, `"`, `""`) + `"` }

//go:embed queries/list_queries.sql
var listQueries string

// QuerySorts maps the sort keys of the workload list to their expressions.
var QuerySorts = map[string]string{
	"total_time":   "cur.total_ms",
	"calls":        "cur.calls",
	"mean_time":    "cur.total_ms / NULLIF(cur.calls, 0)",
	"rows":         "cur.rows",
	"shared_read":  "cur.rd",
	"temp_written": "cur.temp",
	"wal_bytes":    "cur.wal",
	"mean_ratio":   "(cur.total_ms / NULLIF(cur.calls, 0)) / NULLIF(prev.total_ms / NULLIF(prev.calls, 0), 0)",
	"calls_ratio":  "cur.calls::float8 / NULLIF(prev.calls, 0)",
	"total_ratio":  "cur.total_ms / NULLIF(prev.total_ms, 0)",
}

// ListQueries is the workload list sorted by a key of QuerySorts (total time
// for an unknown key).
func ListQueries(sort string, desc bool) string {
	order, ok := QuerySorts[sort]
	if !ok {
		order = QuerySorts["total_time"]
	}
	dir := "ASC"
	if desc {
		dir = "DESC"
	}
	return fill(listQueries, "order", order, "direction", dir)
}

//go:embed load/load_rows.sql
var loadRows string

// LoadDimensions are the columns of each grouping of load samples. Columns
// wrapped in max() are labels: selected, not grouped by.
var LoadDimensions = map[string][]string{
	"class":       {},
	"event":       {"wait_event"},
	"query":       {"coalesce(dbid::bigint, 0)", "coalesce(userid::bigint, 0)", "coalesce(queryid, 0)", "max(datname)", "max(usename)"},
	"user":        {"usename"},
	"database":    {"datname"},
	"application": {"application_name"},
	"backend":     {"backend_type"},
}

// LoadRows groups load samples by a dimension of LoadDimensions.
func LoadRows(dimension string) (string, error) {
	cols, ok := LoadDimensions[dimension]
	if !ok {
		return "", fmt.Errorf("unknown load dimension %q", dimension)
	}
	key, group := "ARRAY[]::text[]", ""
	if len(cols) > 0 {
		sel := make([]string, len(cols))
		for i, c := range cols {
			sel[i] = c + "::text"
			if !strings.HasPrefix(c, "max(") {
				group += ", " + c
			}
		}
		key = "ARRAY[" + strings.Join(sel, ", ") + "]"
	}
	return fill(loadRows, "key", key, "group", group), nil
}

//go:embed series/raw_series.sql
var rawSeries string

//go:embed series/rollup_series.sql
var rollupSeries string

// RawSeries reads one metric from raw points with an aggregation (see
// repository.MetricAggregation).
func RawSeries(aggregation string) string {
	expr := "(array_agg(value ORDER BY ts DESC))[1]"
	switch aggregation {
	case "sum", "max":
		expr = aggregation + "(value)"
	case "mean_interval_ratio":
		expr = "avg(value)"
	case "time_weighted_mean":
		expr = "CASE WHEN count(interval_seconds)=count(*) THEN sum(value * interval_seconds) / NULLIF(sum(interval_seconds),0) END"
	}
	return fill(rawSeries, "aggregate", expr)
}

// Rollup tables by resolution.
const (
	MinuteRollupTable = "metric_rollups_1m"
	HourRollupTable   = "metric_rollups_1h"
)

// RollupSeries reads one metric from a rollup table, aggregating rollup rows
// like RawSeries aggregates raw points.
func RollupSeries(table, aggregation string) string {
	expr := "(array_agg(vlast ORDER BY last_ts DESC))[1]"
	switch aggregation {
	case "sum":
		expr = "sum(vsum)"
	case "max":
		expr = "max(vmax)"
	case "mean_interval_ratio":
		expr = "sum(vsum) / NULLIF(sum(known), 0)"
	case "time_weighted_mean":
		expr = "CASE WHEN sum(with_interval) = sum(samples) THEN sum(wsum) / NULLIF(sum(wint), 0) END"
	}
	if table != HourRollupTable {
		table = MinuteRollupTable
	}
	return fill(rollupSeries, "table", table, "aggregate", expr)
}

//go:embed retention/delete_older_than.sql
var deleteOlderThan string

// HistoryTables are pruned by their ts column after the history retention;
// RollupTables after the baseline retention.
var (
	HistoryTables = []string{"metric_points", "query_samples", "statement_collections", "activity_snapshots",
		"table_stats", "index_stats", "state_snapshots", "load_samples", "load_buckets", "log_events"}
	RollupTables       = []string{"query_rollups", "statement_coverage"}
	MetricRollupTables = []string{MinuteRollupTable, HourRollupTable}
)

// DeleteOlderThan deletes a batch of rows of a table of HistoryTables,
// RollupTables, or MetricRollupTables older than $1.
func DeleteOlderThan(table string) string {
	return fill(deleteOlderThan, "table", quoteIdent(table))
}

//go:embed targets/delete_target_rows.sql
var deleteTargetRows string

// PurgeTables hold a target's history; purging a target deletes their rows in
// batches before the target row.
var PurgeTables = []string{
	"metric_points", "query_samples", "statement_collections", "activity_snapshots",
	"table_stats", "index_stats", "state_snapshots", "collection_gaps", "findings",
	"query_rollups", "statement_coverage", "query_plans", "metric_rollups_1m", "metric_rollups_1h", "recommendations", "catalog_state",
	"change_events", "change_state", "log_events", "log_agents", "live_state",
}

// DeleteTargetRows deletes a batch of a target's rows of a table of
// PurgeTables (or incidents).
func DeleteTargetRows(table string) string {
	return fill(deleteTargetRows, "table", quoteIdent(table))
}

//go:embed timescale/enable_compression.sql
var enableCompression string

// EnableCompression turns on compression of a hypertable segmented by the
// given columns (from the fixed hypertable list).
func EnableCompression(table, segmentBy string) string {
	return fill(enableCompression, "table", quoteIdent(table), "segment_by", segmentBy)
}
