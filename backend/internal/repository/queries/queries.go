package queries

import _ "embed"

//go:embed queries/command_totals.sql
var CommandTotals string

//go:embed queries/command_buckets.sql
var CommandBuckets string

//go:embed queries/command_coverage_buckets.sql
var CommandCoverageBuckets string

//go:embed queries/statement_coverage.sql
var StatementCoverage string

//go:embed queries/query_identity.sql
var QueryIdentity string

//go:embed queries/query_refs.sql
var QueryRefs string

//go:embed queries/query_buckets.sql
var QueryBuckets string

//go:embed queries/query_rollup_buckets.sql
var QueryRollupBuckets string

//go:embed queries/window_totals.sql
var WindowTotals string

//go:embed queries/identity_key.sql
var IdentityKey string
