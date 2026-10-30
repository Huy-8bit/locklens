package pglog

import (
	"encoding/json"
	"strconv"
)

// jsonRecord is a jsonlog line.
type jsonRecord struct {
	Timestamp   string          `json:"timestamp"`
	User        string          `json:"user"`
	DBName      string          `json:"dbname"`
	PID         int             `json:"pid"`
	SessionID   string          `json:"session_id"`
	LineNum     int64           `json:"line_num"`
	Severity    string          `json:"error_severity"`
	StateCode   string          `json:"state_code"`
	Message     string          `json:"message"`
	Detail      string          `json:"detail"`
	Hint        string          `json:"hint"`
	Context     string          `json:"context"`
	Statement   string          `json:"statement"`
	Application string          `json:"application_name"`
	BackendType string          `json:"backend_type"`
	QueryID     json.RawMessage `json:"query_id"`
}

// ParseJSON reads one jsonlog line.
func ParseJSON(line []byte) (Record, error) {
	var j jsonRecord
	if err := json.Unmarshal(line, &j); err != nil {
		return Record{}, err
	}
	r := Record{Time: parseTime(j.Timestamp), PID: j.PID, User: j.User, Database: j.DBName, Application: j.Application,
		BackendType: j.BackendType, SessionID: j.SessionID, SessionLine: j.LineNum, Severity: j.Severity, SQLState: j.StateCode,
		Message: j.Message, Detail: j.Detail, Hint: j.Hint, Context: j.Context, Statement: j.Statement}
	if len(j.QueryID) > 0 {
		r.QueryID, _ = strconv.ParseInt(string(trimQuotes(j.QueryID)), 10, 64)
	}
	return r, nil
}

func trimQuotes(b []byte) []byte {
	if len(b) >= 2 && b[0] == '"' && b[len(b)-1] == '"' {
		return b[1 : len(b)-1]
	}
	return b
}
