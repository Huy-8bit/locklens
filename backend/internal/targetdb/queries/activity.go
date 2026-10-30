package queries

import _ "embed"

//go:embed activity/activity_counts.sql
var ActivityCounts string

//go:embed activity/vacuum_progress.sql
var VacuumProgress string

//go:embed activity/connection_groups.sql
var ConnectionGroups string
