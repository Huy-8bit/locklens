package queries

import _ "embed"

//go:embed doctor/read_activity.sql
var ReadActivity string

//go:embed doctor/read_locks.sql
var ReadLocks string

//go:embed doctor/blocking_pids.sql
var BlockingPids string

//go:embed doctor/read_prepared.sql
var ReadPrepared string
