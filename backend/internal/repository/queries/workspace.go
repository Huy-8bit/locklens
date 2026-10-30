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

//go:embed workspace/topology_links.sql
var TopologyLinks string

//go:embed workspace/set_topology_link.sql
var SetTopologyLink string

//go:embed workspace/delete_topology_link.sql
var DeleteTopologyLink string

//go:embed workspace/history_bounds.sql
var HistoryBounds string
