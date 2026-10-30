package queries

import _ "embed"

//go:embed catalog/catalog_tables.sql
var CatalogTables string

//go:embed catalog/catalog_indexes.sql
var CatalogIndexes string

//go:embed catalog/catalog_f_ks.sql
var CatalogFKs string

//go:embed catalog/catalog_columns.sql
var CatalogColumns string

//go:embed catalog/catalog_database.sql
var CatalogDatabase string

//go:embed catalog/catalog_extensions.sql
var CatalogExtensions string

//go:embed catalog/catalog_settings.sql
var CatalogSettings string
