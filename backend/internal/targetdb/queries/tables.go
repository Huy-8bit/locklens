package queries

import _ "embed"

//go:embed tables/table_stats_exact.sql
var TableStatsExact string

//go:embed tables/table_stats_estimate.sql
var TableStatsEstimate string

//go:embed tables/index_stats_exact.sql
var IndexStatsExact string

//go:embed tables/index_stats_estimate.sql
var IndexStatsEstimate string

//go:embed tables/database_ages.sql
var DatabaseAges string

//go:embed tables/vacuum_settings.sql
var VacuumSettings string
