package queries

import _ "embed"

//go:embed logs/insert_log_event.sql
var InsertLogEvent string

//go:embed logs/upsert_log_agent.sql
var UpsertLogAgent string

//go:embed logs/list_log_agents.sql
var ListLogAgents string

//go:embed logs/list_log_events.sql
var ListLogEvents string

//go:embed logs/log_counts.sql
var LogCounts string

//go:embed logs/log_statements.sql
var LogStatements string
