package queries

import (
	_ "embed"
	"strings"
)

//go:embed statements/statement_counters.sql
var statementCounters string

//go:embed statements/statement_texts.sql
var statementTexts string

//go:embed statements/statements_info.sql
var statementsInfo string

// StatementCounters reads pg_stat_statements counters; function is the
// sanitized, schema-qualified function name.
func StatementCounters(function string) string {
	return strings.ReplaceAll(statementCounters, "{{function}}", function)
}

// StatementTexts reads the texts of some queryids.
func StatementTexts(function string) string {
	return strings.ReplaceAll(statementTexts, "{{function}}", function)
}

// StatementsInfo reads pg_stat_statements_info; view is sanitized and
// schema-qualified.
func StatementsInfo(view string) string {
	return strings.ReplaceAll(statementsInfo, "{{view}}", view)
}
