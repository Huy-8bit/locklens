package queries

import _ "embed"

//go:embed managed_roles/managed_roles.sql
var ManagedRoles string

//go:embed managed_roles/put_managed_role.sql
var PutManagedRole string
