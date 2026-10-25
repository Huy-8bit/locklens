package auth

import "slices"

// Role is what a user may do.
type Role string

const (
	// RoleAdmin may do everything, including managing users, targets, and settings.
	RoleAdmin Role = "admin"
	// RoleOperator reads everything, browses table rows, runs read-only SQL,
	// and acknowledges incidents.
	RoleOperator Role = "operator"
	// RoleViewer reads monitoring data only.
	RoleViewer Role = "viewer"
)

// Roles in order of increasing privilege.
var Roles = []Role{RoleViewer, RoleOperator, RoleAdmin}

// ValidRole reports whether r names a role.
func ValidRole(r string) bool {
	return slices.Contains(Roles, Role(r))
}

// Permission is one capability checked by the API.
type Permission string

const (
	// PermView reads monitoring data: targets, metrics, queries, sessions,
	// incidents, settings. Every role has it.
	PermView Permission = "view"
	// PermBrowseStructure reads catalog metadata through the workspace.
	PermBrowseStructure Permission = "workspace.structure"
	// PermBrowseData reads table rows through the workspace.
	PermBrowseData Permission = "workspace.data"
	// PermRunSQL runs read-only SQL and EXPLAIN through the workspace.
	PermRunSQL Permission = "workspace.sql"
	// PermAcknowledge acknowledges incidents.
	PermAcknowledge Permission = "incidents.acknowledge"
	// PermManageTargets adds, edits, tests, and removes targets and their
	// investigation access.
	PermManageTargets Permission = "targets.manage"
	// PermManageSettings changes rule thresholds and topology mappings and
	// imports incidents.
	PermManageSettings Permission = "settings.manage"
	// PermManageUsers adds, edits, and removes users.
	PermManageUsers Permission = "users.manage"
)

var grants = map[Role][]Permission{
	RoleViewer:   {PermView, PermBrowseStructure},
	RoleOperator: {PermView, PermBrowseStructure, PermBrowseData, PermRunSQL, PermAcknowledge},
	RoleAdmin: {PermView, PermBrowseStructure, PermBrowseData, PermRunSQL, PermAcknowledge,
		PermManageTargets, PermManageSettings, PermManageUsers},
}

// Can reports whether the role has a permission. Unknown roles have none.
func (r Role) Can(p Permission) bool {
	return slices.Contains(grants[r], p)
}

// Permissions lists the role's permissions (for the UI).
func (r Role) Permissions() []Permission {
	return slices.Clone(grants[r])
}
