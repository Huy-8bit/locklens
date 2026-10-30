package queries

import _ "embed"

//go:embed auth/managed_admin_password_hash.sql
var ManagedAdminPasswordHash string

//go:embed auth/upsert_managed_admin_user.sql
var UpsertManagedAdminUser string

//go:embed auth/delete_other_managed_admins.sql
var DeleteOtherManagedAdmins string

//go:embed auth/delete_sessions_of_user.sql
var DeleteSessionsOfUser string

//go:embed auth/delete_user.sql
var DeleteUser string

//go:embed auth/touch_login.sql
var TouchLogin string

//go:embed auth/create_session.sql
var CreateSession string

//go:embed auth/session_by_hash.sql
var SessionByHash string

//go:embed auth/delete_session.sql
var DeleteSession string

//go:embed auth/user_by_username.sql
var UserByUsername string

//go:embed auth/user_by_i_d.sql
var UserByID string

//go:embed auth/list_users.sql
var ListUsers string

//go:embed auth/create_user.sql
var CreateUser string

//go:embed auth/update_user.sql
var UpdateUser string
