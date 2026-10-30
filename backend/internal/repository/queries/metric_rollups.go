package queries

import _ "embed"

//go:embed metric_rollups/rollup_metrics_minute.sql
var RollupMetricsMinute string

//go:embed metric_rollups/rollup_metrics_hour.sql
var RollupMetricsHour string

//go:embed metric_rollups/metric_rollup_watermark.sql
var MetricRollupWatermark string
