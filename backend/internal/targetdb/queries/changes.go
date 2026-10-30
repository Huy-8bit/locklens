package queries

import _ "embed"

//go:embed changes/process_facts.sql
var ProcessFacts string

//go:embed changes/settings.sql
var Settings string
