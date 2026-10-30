package queries

import (
	_ "embed"
	"strings"
)

//go:embed onboard/setup_facts.sql
var SetupFacts string

//go:embed onboard/system_identifier.sql
var SystemIdentifier string

//go:embed onboard/existing_roles.sql
var ExistingRoles string

//go:embed onboard/create_roles.sql
var createRoles string

// CreateRoles returns the role statements, one per line of the template, with
// the placeholders filled by already quoted identifiers and literals.
func CreateRoles(values map[string]string) []string {
	pairs := make([]string, 0, 2*len(values))
	for k, v := range values {
		pairs = append(pairs, "{{"+k+"}}", v)
	}
	filled := strings.NewReplacer(pairs...).Replace(createRoles)
	var out []string
	for _, line := range strings.Split(filled, "\n") {
		if line = strings.TrimSpace(line); line != "" && !strings.HasPrefix(line, "--") {
			out = append(out, line)
		}
	}
	return out
}
