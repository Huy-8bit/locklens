package investigate

import (
	"context"
	"encoding/json"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
)

// Bounds of every result.
const (
	MaxRows  = 500
	MaxBytes = 1 << 20
	MaxCell  = 8192
)

type Column struct {
	Name string `json:"name"`
	OID  uint32 `json:"type_oid"`
}

type Result struct {
	Columns       []Column    `json:"columns"`
	Rows          [][]*string `json:"rows"`
	Truncated     bool        `json:"truncated"`
	CellTruncated bool        `json:"cell_truncated"`
	Bytes         int         `json:"bytes"`
	DurationMS    int64       `json:"duration_ms"`
	Pagination    string      `json:"pagination,omitempty"`
	Next          *string     `json:"next,omitempty"`
}

func readResult(ctx context.Context, tx pgx.Tx, query string, args ...any) (Result, error) {
	start := time.Now()
	r := Result{Columns: []Column{}, Rows: [][]*string{}}
	rows, err := tx.Query(ctx, query, append([]any{pgx.QueryResultFormats{pgx.TextFormatCode}}, args...)...)
	if err != nil {
		return r, err
	}
	defer rows.Close()
	if len(rows.FieldDescriptions()) > 100 {
		return r, fail("too_many_columns", "Results are limited to 100 columns.")
	}
	for _, f := range rows.FieldDescriptions() {
		r.Columns = append(r.Columns, Column{f.Name, f.DataTypeOID})
	}
	header, _ := json.Marshal(r.Columns)
	r.Bytes = len(header) + 256
	for rows.Next() {
		if len(r.Rows) >= MaxRows {
			r.Truncated = true
			break
		}
		values := rows.RawValues()
		row := make([]*string, len(values))
		for i, v := range values {
			if v != nil {
				if len(v) > MaxCell {
					v = v[:MaxCell]
					for !utf8.Valid(v) {
						v = v[:len(v)-1]
					}
					r.CellTruncated = true
				}
				s := string(v)
				row[i] = &s
			}
		}
		encoded, _ := json.Marshal(row)
		size := len(encoded) + 1
		if r.Bytes+size > MaxBytes-8192 {
			r.Truncated = true
			break
		}
		r.Rows = append(r.Rows, row)
		r.Bytes += size
	}
	if err = rows.Err(); err != nil {
		return r, err
	}
	r.DurationMS = time.Since(start).Milliseconds()
	return r, nil
}
