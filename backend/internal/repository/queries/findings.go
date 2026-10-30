package queries

import _ "embed"

//go:embed findings/upsert_finding.sql
var UpsertFinding string

//go:embed findings/resolve_missing_findings.sql
var ResolveMissingFindings string

//go:embed findings/link_findings_to_incident.sql
var LinkFindingsToIncident string

//go:embed findings/list_findings.sql
var ListFindings string
