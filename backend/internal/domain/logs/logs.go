// Package logs turns PostgreSQL server log records into events LockLens
// keeps: slow statements and their plans (auto_explain), lock waits,
// deadlocks, autovacuum and autoanalyze runs, temporary files, checkpoints,
// and errors. It runs in the log agent, next to the logs, so statement
// texts are normalised (constants replaced by $n) and messages redacted
// before anything is sent. On the server it validates what agents send and
// summarises the stored events.
//
// Durations from the slow statement log describe only the executions slower
// than log_min_duration_statement (or auto_explain.log_min_duration): their
// percentiles are percentiles of those executions, not of all executions,
// unless the threshold is 0.
package logs

import "github.com/locklens/locklens/internal/domain/observe"

// Event kinds.
const (
	KindSlowQuery   = "slow_query"  // log_min_duration_statement
	KindPlan        = "plan"        // auto_explain
	KindLockWait    = "lock_wait"   // log_lock_waits
	KindDeadlock    = "deadlock"    // deadlock detected
	KindAutovacuum  = "autovacuum"  // log_autovacuum_min_duration
	KindAutoanalyze = "autoanalyze" // log_autovacuum_min_duration
	KindTempFile    = "temp_file"   // log_temp_files
	KindCheckpoint  = "checkpoint"  // log_checkpoints
	KindError       = "error"       // ERROR, FATAL, PANIC
)

// Kinds are every event kind.
var Kinds = []string{KindSlowQuery, KindPlan, KindLockWait, KindDeadlock, KindAutovacuum, KindAutoanalyze, KindTempFile, KindCheckpoint, KindError}

// Event is one log event.
type Event = observe.LogEvent
