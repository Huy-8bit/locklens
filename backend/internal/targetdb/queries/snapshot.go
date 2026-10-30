package queries

import _ "embed"

//go:embed snapshot/server.sql
var Server string

//go:embed snapshot/blocking.sql
var Blocking string

//go:embed snapshot/waiting_locks.sql
var WaitingLocks string

//go:embed snapshot/sessions.sql
var Sessions string

//go:embed snapshot/prepared.sql
var Prepared string
