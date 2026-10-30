package queries

import _ "embed"

//go:embed targets/insert_target_status.sql
var InsertTargetStatus string

//go:embed targets/soft_delete_target.sql
var SoftDeleteTarget string

//go:embed targets/disable_target_status.sql
var DisableTargetStatus string

//go:embed targets/target_exists.sql
var TargetExists string

//go:embed targets/disable_target_for_purge.sql
var DisableTargetForPurge string

//go:embed targets/delete_target.sql
var DeleteTarget string

//go:embed targets/save_target_status.sql
var SaveTargetStatus string

//go:embed targets/open_incidents_by_target.sql
var OpenIncidentsByTarget string

//go:embed targets/active_findings_by_target.sql
var ActiveFindingsByTarget string

//go:embed targets/create_target.sql
var CreateTarget string

//go:embed targets/update_target.sql
var UpdateTarget string

//go:embed targets/get_target.sql
var GetTarget string

//go:embed targets/get_target_by_name.sql
var GetTargetByName string

//go:embed targets/list_targets.sql
var ListTargets string

//go:embed targets/get_target_status.sql
var GetTargetStatus string

//go:embed targets/list_target_statuses.sql
var ListTargetStatuses string
