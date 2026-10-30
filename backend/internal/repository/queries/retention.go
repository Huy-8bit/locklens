package queries

import _ "embed"

//go:embed retention/drop_chunks_older_than.sql
var DropChunksOlderThan string

//go:embed retention/purge_old_plans.sql
var PurgeOldPlans string

//go:embed retention/purge_unreferenced_identities.sql
var PurgeUnreferencedIdentities string

//go:embed retention/purge_resolved_incident_events.sql
var PurgeResolvedIncidentEvents string

//go:embed retention/purge_resolved_incidents.sql
var PurgeResolvedIncidents string

//go:embed retention/purge_resolved_findings.sql
var PurgeResolvedFindings string

//go:embed retention/purge_closed_recommendations.sql
var PurgeClosedRecommendations string

//go:embed retention/purge_old_gaps.sql
var PurgeOldGaps string

//go:embed retention/purge_old_deliveries.sql
var PurgeOldDeliveries string

//go:embed retention/purge_ended_silences.sql
var PurgeEndedSilences string

//go:embed retention/purge_expired_sessions.sql
var PurgeExpiredSessions string

//go:embed retention/purge_deleted_targets.sql
var PurgeDeletedTargets string

//go:embed retention/table_sizes.sql
var TableSizes string

//go:embed retention/table_sizes_timescale.sql
var TableSizesTimescale string

//go:embed retention/purge_old_changes.sql
var PurgeOldChanges string
