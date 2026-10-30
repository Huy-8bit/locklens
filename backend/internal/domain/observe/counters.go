package observe

import "time"

// CounterSample is one reading of cumulative counters from one source.
//
// Values are keyed first by scope (for example a database OID in
// pg_stat_database, or "" for a cluster-wide view) and then by counter name.
// Resets are tracked per scope.
type CounterSample struct {
	At time.Time
	// ServerStart is pg_postmaster_start_time(); a change means the server
	// restarted and every counter started again from zero.
	ServerStart time.Time
	Scopes      map[string]ScopeCounters
}

// ScopeCounters holds the counters of one scope.
type ScopeCounters struct {
	// StatsReset is the scope's stats_reset timestamp, if the view has one.
	StatsReset *time.Time
	Values     map[string]float64
}

// StatementCounters are the cumulative counters of one pg_stat_statements
// entry.
type StatementCounters struct {
	Calls   int64
	TotalMS float64
	Rows    int64
	BlkHit  int64
	BlkRead int64
	TempW   int64
	WAL     *float64
}

// TableCounters are the cumulative access counters of one table
// (pg_stat_user_tables).
type TableCounters struct {
	Schema, Name                                string
	SeqScan, SeqRead, IdxScan, IdxFetch, Writes int64
	Dead, Live                                  int64
}
