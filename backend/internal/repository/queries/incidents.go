package queries

import _ "embed"

//go:embed incidents/create_incident.sql
var CreateIncident string

//go:embed incidents/insert_incident_event.sql
var InsertIncidentEvent string

//go:embed incidents/add_incident_event_totals.sql
var AddIncidentEventTotals string

//go:embed incidents/append_incident_events.sql
var AppendIncidentEvents string

//go:embed incidents/update_incident_state.sql
var UpdateIncidentState string

//go:embed incidents/list_incident_events.sql
var ListIncidentEvents string

//go:embed incidents/count_open_incidents.sql
var CountOpenIncidents string

//go:embed incidents/acknowledge_incident.sql
var AcknowledgeIncident string

//go:embed incidents/get_incident.sql
var GetIncident string

//go:embed incidents/list_incidents.sql
var ListIncidents string

//go:embed incidents/open_incidents.sql
var OpenIncidents string

//go:embed incidents/latest_resolved_incident.sql
var LatestResolvedIncident string

//go:embed incidents/open_unacknowledged_incidents.sql
var OpenUnacknowledgedIncidents string
