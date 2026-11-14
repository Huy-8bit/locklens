package config

import (
	"fmt"
	"slices"
	"strings"
)

// Roles is what a server does. A server with every role is the default and
// behaves as servers did before roles existed.
type Roles struct {
	// API serves the HTTP API, the event stream, and the instances' metrics.
	API bool
	// Collector collects targets (by lease), records incidents, and writes
	// collected data.
	Collector bool
	// Jobs runs the once-per-cluster jobs: retention, rollups, baselines,
	// the optimizer, notification reminders.
	Jobs bool
}

// AllRoles is a server that does everything.
var AllRoles = Roles{API: true, Collector: true, Jobs: true}

// All reports whether the server has every role.
func (r Roles) All() bool { return r == AllRoles }

// List returns the role names, in a fixed order.
func (r Roles) List() []string {
	var out []string
	if r.API {
		out = append(out, "api")
	}
	if r.Collector {
		out = append(out, "collector")
	}
	if r.Jobs {
		out = append(out, "jobs")
	}
	return out
}

// ParseRoles reads LOCKLENS_ROLES: empty or "all", or a comma-separated list
// of api, collector, jobs.
func ParseRoles(s string) (Roles, error) {
	s = strings.TrimSpace(strings.ToLower(s))
	if s == "" || s == "all" {
		return AllRoles, nil
	}
	var r Roles
	for _, part := range strings.Split(s, ",") {
		switch p := strings.TrimSpace(part); p {
		case "api":
			r.API = true
		case "collector":
			r.Collector = true
		case "jobs":
			r.Jobs = true
		case "all":
			return Roles{}, fmt.Errorf("LOCKLENS_ROLES: \"all\" cannot be combined with other roles")
		default:
			valid := []string{"all", "api", "collector", "jobs"}
			return Roles{}, fmt.Errorf("LOCKLENS_ROLES: unknown role %q (use %s)", p, strings.Join(valid, ", "))
		}
	}
	if !slices.Contains([]bool{r.API, r.Collector, r.Jobs}, true) {
		return Roles{}, fmt.Errorf("LOCKLENS_ROLES: no role given")
	}
	return r, nil
}
