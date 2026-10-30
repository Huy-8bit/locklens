package queries

import _ "embed"

//go:embed collected/insert_points.sql
var InsertPoints string

//go:embed collected/upsert_query_identity.sql
var UpsertQueryIdentity string

//go:embed collected/insert_query_sample.sql
var InsertQuerySample string

//go:embed collected/insert_statement_collection.sql
var InsertStatementCollection string

//go:embed collected/clear_query_texts.sql
var ClearQueryTexts string

//go:embed collected/insert_activity_snapshot.sql
var InsertActivitySnapshot string

//go:embed collected/insert_table_stat.sql
var InsertTableStat string

//go:embed collected/insert_index_stat.sql
var InsertIndexStat string

//go:embed collected/insert_table_health_state.sql
var InsertTableHealthState string

//go:embed collected/insert_state_snapshot.sql
var InsertStateSnapshot string

//go:embed collected/insert_gap.sql
var InsertGap string
