package queries

import _ "embed"

//go:embed maintenance/list_maintenance_windows.sql
var ListMaintenanceWindows string

//go:embed maintenance/insert_maintenance_window.sql
var InsertMaintenanceWindow string

//go:embed maintenance/update_maintenance_window.sql
var UpdateMaintenanceWindow string

//go:embed maintenance/delete_maintenance_window.sql
var DeleteMaintenanceWindow string
