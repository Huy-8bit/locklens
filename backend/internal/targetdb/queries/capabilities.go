package queries

import (
	_ "embed"
	"strings"
)

//go:embed capabilities/server_facts.sql
var ServerFacts string

//go:embed capabilities/statements_extension.sql
var StatementsExtension string

//go:embed capabilities/statements_probe.sql
var statementsProbe string

// StatementsProbe reads one row of pg_stat_statements; view is sanitized and
// schema-qualified.
func StatementsProbe(view string) string {
	return strings.ReplaceAll(statementsProbe, "{{view}}", view)
}
