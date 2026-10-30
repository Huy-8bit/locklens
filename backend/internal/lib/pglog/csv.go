package pglog

import (
	"bytes"
	"encoding/csv"
	"errors"
	"io"
	"strconv"
)

// csvlog columns (PostgreSQL 13 added backend_type; 14 leader_pid and
// query_id).
const (
	csvTime = iota
	csvUser
	csvDatabase
	csvPID
	csvConnFrom
	csvSessionID
	csvSessionLine
	csvCommandTag
	csvSessionStart
	csvVXID
	csvXID
	csvSeverity
	csvSQLState
	csvMessage
	csvDetail
	csvHint
	csvInternalQuery
	csvInternalQueryPos
	csvContext
	csvQuery
	csvQueryPos
	csvLocation
	csvApplication
	csvBackendType
	csvLeaderPID
	csvQueryID
)

// CSVReader reads csvlog records; a record may span several lines.
type CSVReader struct{ r *csv.Reader }

// NewCSVReader reads csvlog from r.
func NewCSVReader(r io.Reader) *CSVReader {
	c := csv.NewReader(r)
	c.FieldsPerRecord = -1
	c.LazyQuotes = true
	c.ReuseRecord = true
	return &CSVReader{r: c}
}

// Read returns the next record; io.EOF at the end.
func (c *CSVReader) Read() (Record, error) {
	for {
		f, err := c.r.Read()
		if err != nil {
			var pe *csv.ParseError
			if errors.As(err, &pe) {
				continue // skip a malformed record
			}
			return Record{}, err
		}
		if r, ok := csvRecord(f); ok {
			return r, nil
		}
	}
}

// ParseCSV reads the complete records at the start of data (a file being
// written may end in the middle of one) and returns how many bytes they
// used.
func ParseCSV(data []byte) ([]Record, int) {
	end := bytes.LastIndexByte(data, '\n')
	if end < 0 {
		return nil, 0
	}
	r := csv.NewReader(bytes.NewReader(data[:end+1]))
	r.FieldsPerRecord = -1
	// Strict quotes: an unterminated quoted field is a record the server is
	// still writing.
	var out []Record
	used := 0
	for {
		f, err := r.Read()
		if err != nil {
			var pe *csv.ParseError
			if errors.As(err, &pe) && pe.Err != csv.ErrQuote && pe.Err != csv.ErrBareQuote {
				used = int(r.InputOffset()) // skip a malformed record
				continue
			}
			// io.EOF, or a quoted field that continues after data: the rest
			// is read later.
			return out, used
		}
		used = int(r.InputOffset())
		if rec, ok := csvRecord(f); ok {
			out = append(out, rec)
		}
	}
}

// csvRecord maps csvlog fields to a record.
func csvRecord(f []string) (Record, bool) {
	{
		if len(f) <= csvApplication {
			return Record{}, false
		}
		get := func(i int) string {
			if i < len(f) {
				return f[i]
			}
			return ""
		}
		r := Record{Time: parseTime(f[csvTime]), User: f[csvUser], Database: f[csvDatabase], SessionID: f[csvSessionID],
			Severity: f[csvSeverity], SQLState: f[csvSQLState], Message: f[csvMessage], Detail: f[csvDetail], Hint: f[csvHint],
			Context: f[csvContext], Statement: f[csvQuery], Application: f[csvApplication], BackendType: get(csvBackendType)}
		r.PID, _ = strconv.Atoi(f[csvPID])
		r.SessionLine, _ = strconv.ParseInt(f[csvSessionLine], 10, 64)
		r.QueryID, _ = strconv.ParseInt(get(csvQueryID), 10, 64)
		return r, true
	}
}
