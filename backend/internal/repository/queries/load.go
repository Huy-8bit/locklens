package queries

import _ "embed"

//go:embed load/insert_load_bucket_ticks.sql
var InsertLoadBucketTicks string

//go:embed load/insert_load_sample.sql
var InsertLoadSample string

//go:embed load/load_ticks.sql
var LoadTicks string

//go:embed load/query_refs_by_activity.sql
var QueryRefsByActivity string
