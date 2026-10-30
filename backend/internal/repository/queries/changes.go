package queries

import _ "embed"

//go:embed changes/insert_change.sql
var InsertChange string

//go:embed changes/list_changes.sql
var ListChanges string

//go:embed changes/delete_change.sql
var DeleteChange string

//go:embed changes/change_state.sql
var ChangeState string

//go:embed changes/save_change_state.sql
var SaveChangeState string
