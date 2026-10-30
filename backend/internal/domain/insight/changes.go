package insight

import (
	"fmt"
	"strings"
	"time"

	"github.com/locklens/locklens/internal/domain/changes"
	"github.com/locklens/locklens/internal/domain/observe"
)

// changeLead is how long before an anomaly a change counts as a lead: a
// change has its effect soon (a deploy, a dropped index), so older ones
// weigh less and changes after the anomaly began do not count.
const changeLead = 2 * time.Hour

// plannerSettings change execution plans when they change.
var plannerSettings = []string{"work_mem", "random_page_cost", "seq_page_cost", "effective_cache_size", "default_statistics_target",
	"jit", "enable_", "plan_cache_mode", "from_collapse_limit", "join_collapse_limit", "cpu_", "max_parallel_workers_per_gather",
	"parallel_", "geqo"}

// weighChanges adds the evidence of changes before the anomaly: a reported
// deploy or migration is a hypothesis of its own; schema and planner
// setting changes support a plan change; a restart supports resource
// pressure (cold caches).
func weighChanges(sig Signals, add func(id, title string, next ...string) *hyp, plan, sat *hyp) {
	var recent *hyp
	for _, c := range sig.Changes {
		before := sig.Since.Sub(c.At)
		if before < -time.Minute || before > changeLead {
			continue
		}
		// Within minutes counts fully; two hours before counts a fifth.
		w := 1 - 0.8*min(1, max(0, before.Hours()/changeLead.Hours()))
		ago := fmt.Sprintf("%s before the anomaly", before.Round(time.Minute))
		if before < time.Minute {
			ago = "at about the same time as the anomaly"
		}
		switch c.Kind {
		case changes.KindDeploy, changes.KindMigration, changes.KindConfig, changes.KindNote:
			if recent == nil {
				recent = add("recent_change", "A deploy, migration, or configuration change made shortly before",
					"Compare the statement's behaviour before and after the change (statement page, plan history).",
					"Ask the owner of the change whether it touched this statement, its tables, or its parameters.")
			}
			recent.support(0.4*w, "%s (%s, %s).", c.Title, c.Kind, ago)
		case changes.KindSchema:
			if dropsOrInvalidates(c) {
				plan.support(0.45*w, "%s (%s): a dropped or invalid index changes access paths.", c.Title, ago)
			} else {
				plan.support(0.2*w, "%s (%s).", c.Title, ago)
			}
		case changes.KindSettings:
			if names := plannerChanges(c); len(names) > 0 {
				plan.support(0.35*w, "Planner settings changed %s: %s.", ago, strings.Join(names, ", "))
			}
		case changes.KindRestart, changes.KindPromotion, changes.KindDemotion:
			sat.support(0.25*w, "%s (%s): caches start cold and connections reconnect afterwards.", c.Title, ago)
		case changes.KindUpgrade:
			plan.support(0.3*w, "%s (%s): the planner and statistics may behave differently after an upgrade.", c.Title, ago)
		}
	}
}

func dropsOrInvalidates(c observe.ChangeEvent) bool {
	for _, x := range detailList(c) {
		if x["object"] == "index" && (x["change"] == "dropped" || x["change"] == "invalid") {
			return true
		}
	}
	return false
}

func plannerChanges(c observe.ChangeEvent) []string {
	var out []string
	for _, x := range detailList(c) {
		name, _ := x["name"].(string)
		for _, p := range plannerSettings {
			if name == p || strings.HasSuffix(p, "_") && strings.HasPrefix(name, p) {
				out = append(out, name)
				break
			}
		}
	}
	return out
}

// detailList reads details.changes, whether typed (just detected) or
// decoded from JSON (stored).
func detailList(c observe.ChangeEvent) []map[string]any {
	var out []map[string]any
	switch v := c.Details["changes"].(type) {
	case []any:
		for _, x := range v {
			if m, ok := x.(map[string]any); ok {
				out = append(out, m)
			}
		}
	case []changes.ObjectChange:
		for _, x := range v {
			out = append(out, map[string]any{"object": x.Object, "name": x.Name, "change": x.Change})
		}
	case []changes.SettingChange:
		for _, x := range v {
			out = append(out, map[string]any{"name": x.Name})
		}
	}
	return out
}
