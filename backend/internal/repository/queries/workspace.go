package queries

import _ "embed"

//go:embed workspace/workspace_policy.sql
var WorkspacePolicy string

//go:embed workspace/set_workspace_policy.sql
var SetWorkspacePolicy string

//go:embed workspace/insert_workspace_audit.sql
var InsertWorkspaceAudit string

//go:embed workspace/trim_workspace_audit.sql
var TrimWorkspaceAudit string
