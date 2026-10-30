package queries

import _ "embed"

//go:embed snapshots/list_activity_snapshots.sql
var ListActivitySnapshots string

//go:embed snapshots/get_activity_snapshot.sql
var GetActivitySnapshot string

//go:embed snapshots/latest_activity_snapshot.sql
var LatestActivitySnapshot string

//go:embed snapshots/latest_state.sql
var LatestState string

//go:embed snapshots/latest_table_health_states.sql
var LatestTableHealthStates string

//go:embed snapshots/latest_table_stats.sql
var LatestTableStats string

//go:embed snapshots/latest_index_stats.sql
var LatestIndexStats string
