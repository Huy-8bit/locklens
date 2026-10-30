package queries

import _ "embed"

//go:embed investigate/catalog_databases.sql
var CatalogDatabases string

//go:embed investigate/catalog_schemas.sql
var CatalogSchemas string

//go:embed investigate/catalog_relations.sql
var CatalogRelations string

//go:embed investigate/catalog_functions.sql
var CatalogFunctions string

//go:embed investigate/catalog_types.sql
var CatalogTypes string

//go:embed investigate/detail_columns.sql
var DetailColumns string

//go:embed investigate/detail_constraints.sql
var DetailConstraints string

//go:embed investigate/detail_indexes.sql
var DetailIndexes string

//go:embed investigate/detail_triggers.sql
var DetailTriggers string

//go:embed investigate/detail_partitions.sql
var DetailPartitions string

//go:embed investigate/relation_o_i_d.sql
var RelationOID string

//go:embed investigate/data_columns.sql
var DataColumns string

//go:embed investigate/unsafe_role.sql
var UnsafeRole string

//go:embed investigate/set_search_path.sql
var SetSearchPath string

//go:embed investigate/hypo_p_g_version.sql
var HypoPGVersion string

//go:embed investigate/hypo_p_g_create_index.sql
var HypoPGCreateIndex string

//go:embed investigate/hypo_p_g_reset.sql
var HypoPGReset string

//go:embed investigate/savepoint_what_if.sql
var SavepointWhatIf string

//go:embed investigate/rollback_to_savepoint_what_if.sql
var RollbackToSavepointWhatIf string

//go:embed investigate/release_savepoint_what_if.sql
var ReleaseSavepointWhatIf string
