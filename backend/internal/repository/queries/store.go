package queries

import _ "embed"

//go:embed store/server_version_num.sql
var ServerVersionNum string

//go:embed store/advisory_lock.sql
var AdvisoryLock string

//go:embed store/advisory_unlock.sql
var AdvisoryUnlock string

//go:embed store/create_schema_migrations.sql
var CreateSchemaMigrations string

//go:embed store/applied_migrations.sql
var AppliedMigrations string

//go:embed store/record_migration.sql
var RecordMigration string

//go:embed store/schema_version.sql
var SchemaVersion string
