package observe

import "time"

// LogEvent is one event read from a PostgreSQL server log by the log agent
// (see package logs). Statement texts are normalised (constants replaced by
// $n) and messages redacted before they leave the database host.
type LogEvent struct {
	// ID identifies the log message, so a batch sent twice is stored once.
	ID          string    `json:"id"`
	At          time.Time `json:"at"`
	Kind        string    `json:"kind"`
	PID         int       `json:"pid,omitempty"`
	DatName     string    `json:"datname,omitempty"`
	UserName    string    `json:"username,omitempty"`
	Application string    `json:"application,omitempty"`
	Severity    string    `json:"severity"`
	SQLState    string    `json:"sqlstate,omitempty"`
	DurationMS  *float64  `json:"duration_ms,omitempty"`
	// QueryID is pg_stat_statements' queryid when the log has it.
	QueryID     *int64         `json:"query_id,omitempty"`
	Fingerprint string         `json:"fingerprint,omitempty"`
	Query       string         `json:"query,omitempty"`
	Message     string         `json:"message"`
	Details     map[string]any `json:"details,omitempty"`
}
