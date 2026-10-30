package queries

import _ "embed"

//go:embed plans/save_plan.sql
var SavePlan string

//go:embed plans/list_plans.sql
var ListPlans string

//go:embed plans/get_plan.sql
var GetPlan string

//go:embed plans/delete_plan.sql
var DeletePlan string

//go:embed plans/count_plans.sql
var CountPlans string
