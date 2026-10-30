package queries

import _ "embed"

//go:embed optimizer/save_catalog.sql
var SaveCatalog string

//go:embed optimizer/latest_catalog.sql
var LatestCatalog string

//go:embed optimizer/list_catalogs.sql
var ListCatalogs string

//go:embed optimizer/prune_catalogs.sql
var PruneCatalogs string

//go:embed optimizer/upsert_recommendation.sql
var UpsertRecommendation string

//go:embed optimizer/resolve_missing_recommendations.sql
var ResolveMissingRecommendations string

//go:embed optimizer/decide_recommendation.sql
var DecideRecommendation string

//go:embed optimizer/mark_applied.sql
var MarkApplied string

//go:embed optimizer/set_verification.sql
var SetVerification string

//go:embed optimizer/set_what_if.sql
var SetWhatIf string

//go:embed optimizer/statement_workload.sql
var StatementWorkload string

//go:embed optimizer/statement_mean_m_s.sql
var StatementMeanMS string

//go:embed optimizer/list_recommendations.sql
var ListRecommendations string

//go:embed optimizer/get_recommendation.sql
var GetRecommendation string
