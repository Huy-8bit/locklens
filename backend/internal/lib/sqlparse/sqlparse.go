// Package sqlparse analyses SQL statements with PostgreSQL's own parser
// (libpg_query through pg_query_go): the tables a statement reads, the
// column predicates an index could serve, the sort order, and lints for
// shapes that often perform badly.
//
// The parser is C code and needs cgo. Built without cgo (CGO_ENABLED=0),
// Available reports false and Analyze returns ErrUnavailable; callers then
// skip parser-based analysis and say so.
package sqlparse

import "errors"

// ErrUnavailable is returned when the binary was built without the parser.
var ErrUnavailable = errors.New("the SQL parser is not available in this build (built without cgo)")

// Table is a relation a statement reads or writes.
type Table struct {
	Schema string `json:"schema,omitempty"`
	Name   string `json:"name"`
	Alias  string `json:"alias,omitempty"`
}

// Qualified is schema.name, or name without a schema.
func (t Table) Qualified() string {
	if t.Schema == "" {
		return t.Name
	}
	return t.Schema + "." + t.Name
}

// Predicate is a condition on one column of one table.
type Predicate struct {
	// Table is the qualified table the column belongs to; empty when it
	// cannot be resolved (an unqualified column with several tables).
	Table  string `json:"table"`
	Column string `json:"column"`
	// Op is =, <, <=, >, >=, <>, IN, LIKE, ILIKE, BETWEEN, IS NULL,
	// IS NOT NULL, or JOIN (an equality with a column of another table).
	Op string `json:"op"`
	// Wrapped names a function applied to the column (lower(email) = $1):
	// a plain index on the column cannot serve it.
	Wrapped string `json:"wrapped,omitempty"`
	// InOr is true when the predicate sits under an OR.
	InOr bool `json:"in_or,omitempty"`
}

// Equality reports whether an index can use the predicate as an equality.
func (p Predicate) Equality() bool {
	return p.Op == "=" || p.Op == "IN" || p.Op == "IS NULL" || p.Op == "JOIN"
}

// Range reports whether an index can use the predicate as a range.
func (p Predicate) Range() bool {
	switch p.Op {
	case "<", "<=", ">", ">=", "BETWEEN":
		return true
	}
	return false
}

// OrderColumn is one ORDER BY column.
type OrderColumn struct {
	Table  string `json:"table"`
	Column string `json:"column"`
	Desc   bool   `json:"desc,omitempty"`
}

// Lint is a statement shape that often performs badly.
type Lint struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	// Severity is info or warning.
	Severity string `json:"severity"`
}

// Lint codes.
const (
	LintSelectStar       = "select_star"
	LintOffset           = "offset_pagination"
	LintLeadingWildcard  = "leading_wildcard"
	LintFunctionOnColumn = "function_on_column"
	LintNotInSubquery    = "not_in_subquery"
	LintOrAcrossColumns  = "or_across_columns"
	LintNoWhere          = "write_without_where"
	LintCartesian        = "cartesian_join"
	LintMultiStatement   = "multiple_statements"
)

// Analysis is what the parser found in a statement.
type Analysis struct {
	// Fingerprint identifies the statement's shape (pg_query fingerprint).
	Fingerprint string `json:"fingerprint"`
	// Command is select, insert, update, delete, merge, or other.
	Command    string        `json:"command"`
	Tables     []Table       `json:"tables"`
	Predicates []Predicate   `json:"predicates"`
	OrderBy    []OrderColumn `json:"order_by"`
	HasLimit   bool          `json:"has_limit"`
	Lints      []Lint        `json:"lints"`
}

// Available reports whether the parser is compiled in.
func Available() bool { return available }
