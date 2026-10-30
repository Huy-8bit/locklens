// Package investigate runs the investigation workspace's operations on a
// monitored database: browsing the catalog, reading rows with a bounded query
// builder, running one read-only statement, EXPLAIN, generic plans, and
// what-if checks with hypothetical indexes. Every function runs inside the
// read-only transaction its caller (domain/workspace) opened, with the
// workspace's own credentials. The static SQL is in targetdb/queries/investigate.
package investigate

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5/pgconn"
)

type Error struct {
	Code     string `json:"code"`
	Message  string `json:"message"`
	Position int32  `json:"position,omitempty"`
}

func (e *Error) Error() string        { return e.Message }
func fail(code, message string) error { return &Error{Code: code, Message: message} }

// Fail returns an Error with a code and a message safe to show.
func Fail(code, message string) error { return fail(code, message) }

func SafeError(err error) *Error {
	var e *Error
	if errors.As(err, &e) {
		return e
	}
	var p *pgconn.PgError
	if errors.As(err, &p) {
		pos := p.Position
		if pos > int32(len(declaration)) {
			pos -= int32(len(declaration))
		}
		return &Error{Code: p.Code, Message: "PostgreSQL rejected the operation. Check permissions, statement syntax and execution limits.", Position: pos}
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return &Error{Code: "cancelled", Message: "Operation cancelled or execution deadline reached."}
	}
	return &Error{Code: "database_error", Message: "Database operation failed or exceeded the protocol size limit."}
}

// declaration opens the cursor every statement runs under; error positions
// are reported relative to the statement.
const declaration = "DECLARE locklens_result NO SCROLL CURSOR FOR "
