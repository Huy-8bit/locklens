package queries

import _ "embed"

//go:embed rollups/rollup_query_samples.sql
var RollupQuerySamples string

//go:embed rollups/rollup_statement_coverage.sql
var RollupStatementCoverage string

//go:embed rollups/rollup_watermark.sql
var RollupWatermark string

//go:embed rollups/active_statements.sql
var ActiveStatements string

//go:embed rollups/baseline_rollup_points.sql
var BaselineRollupPoints string

//go:embed rollups/baseline_coverage_points.sql
var BaselineCoveragePoints string
