package queries

import _ "embed"

//go:embed common/current_database.sql
var CurrentDatabase string

//go:embed common/clock_timestamp.sql
var ClockTimestamp string

//go:embed common/short_lock_timeout.sql
var ShortLockTimeout string

//go:embed common/savepoint.sql
var Savepoint string

//go:embed common/rollback_to_savepoint.sql
var RollbackToSavepoint string

//go:embed common/release_savepoint.sql
var ReleaseSavepoint string
