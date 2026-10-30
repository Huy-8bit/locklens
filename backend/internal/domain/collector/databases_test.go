package collector

import (
	"slices"
	"testing"

	"github.com/locklens/locklens/internal/repository"
)

func TestScopeDatabases(t *testing.T) {
	listed := []string{"app", "billing", "reports"}
	all := repository.Target{DisplayDatabase: "app", DatabaseScope: repository.DatabaseAll}
	if got := scopeDatabases(all, "app", listed); !slices.Equal(got, listed) {
		t.Fatalf("all: %v", got)
	}
	// A list keeps the connected database and the named ones that exist.
	list := repository.Target{DisplayDatabase: "app", DatabaseScope: repository.DatabaseList, Databases: []string{"reports", "gone"}}
	if got := scopeDatabases(list, "app", listed); !slices.Equal(got, []string{"app", "reports"}) {
		t.Fatalf("list: %v", got)
	}
}

func TestScopeNamesConnected(t *testing.T) {
	w := &worker{target: repository.Target{DisplayDatabase: "app"}}
	if names, ok := w.scopeNames([]string{"postgres"}); !ok || !slices.Equal(names, []string{"postgres"}) {
		t.Fatalf("connected scope keeps the catalog just collected: %v %v", names, ok)
	}
	if _, ok := w.scopeNames(nil); ok {
		t.Fatal("nothing collected: nothing is pruned")
	}
	w.target.DatabaseScope = repository.DatabaseAll
	if _, ok := w.scopeNames([]string{"app"}); ok {
		t.Fatal("an unlisted scope is unknown")
	}
}
