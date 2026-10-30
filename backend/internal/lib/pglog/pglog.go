// Package pglog reads PostgreSQL server logs in the three formats the server
// writes (log_destination): jsonlog (PostgreSQL 15+), csvlog, and stderr
// with a log_line_prefix. It turns lines into records; what a record means
// (a slow statement, a lock wait) is decided by its callers.
package pglog

import (
	"strings"
	"time"
)

// Record is one log message with its DETAIL, HINT, CONTEXT, and STATEMENT.
type Record struct {
	Time        time.Time
	PID         int
	User        string
	Database    string
	Application string
	BackendType string
	SessionID   string
	SessionLine int64
	// Severity is LOG, ERROR, FATAL, PANIC, WARNING, NOTICE, INFO, or DEBUGn.
	Severity  string
	SQLState  string
	Message   string
	Detail    string
	Hint      string
	Context   string
	Statement string
	// QueryID is pg_stat_statements' queryid (jsonlog and csvlog, PostgreSQL
	// 14+ with compute_query_id); 0 when unknown.
	QueryID int64
}

// Formats.
const (
	FormatJSON   = "jsonlog"
	FormatCSV    = "csvlog"
	FormatStderr = "stderr"
)

// FormatOf guesses the format of a log file from its name.
func FormatOf(name string) string {
	switch {
	case strings.HasSuffix(name, ".json"):
		return FormatJSON
	case strings.HasSuffix(name, ".csv"):
		return FormatCSV
	}
	return FormatStderr
}

// severities that start a message in stderr logs (the others are parts of
// the previous message).
var severities = map[string]bool{"LOG": true, "ERROR": true, "FATAL": true, "PANIC": true, "WARNING": true, "NOTICE": true,
	"INFO": true, "DEBUG": true, "DEBUG1": true, "DEBUG2": true, "DEBUG3": true, "DEBUG4": true, "DEBUG5": true, "STATEMENT": false}

// parseTime reads the log timestamp formats (%m, %t, csvlog, jsonlog).
func parseTime(s string) time.Time {
	s = strings.TrimSpace(s)
	for _, layout := range []string{"2006-01-02 15:04:05.000 MST", "2006-01-02 15:04:05 MST", "2006-01-02 15:04:05.000 -07", "2006-01-02 15:04:05 -07",
		"2006-01-02 15:04:05.000 -0700", "2006-01-02 15:04:05.000"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t.UTC()
		}
	}
	return time.Time{}
}

func unixFloat(sec float64) time.Time {
	whole := int64(sec)
	return time.Unix(whole, int64((sec-float64(whole))*1e9)).UTC()
}
