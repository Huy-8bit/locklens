package queries

import _ "embed"

//go:embed series/list_gaps.sql
var ListGaps string

//go:embed series/history_bounds.sql
var HistoryBounds string
