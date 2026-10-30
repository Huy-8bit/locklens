package optimizer

import (
	"testing"

	"github.com/locklens/locklens/internal/repository"
)

func TestDatabaseStatements(t *testing.T) {
	stmts := []repository.WorkloadStatement{{IdentityID: 1, DatName: "app"}, {IdentityID: 2, DatName: "reports"}, {IdentityID: 3}}
	ids := func(ss []repository.WorkloadStatement) (out []int64) {
		for _, s := range ss {
			out = append(out, s.IdentityID)
		}
		return out
	}
	// Statements are matched to the catalog of the database they ran in.
	if got := ids(databaseStatements(stmts, "app", false)); len(got) != 1 || got[0] != 1 {
		t.Fatalf("several catalogs: %v", got)
	}
	// With one catalog, statements of an unknown database count too.
	if got := ids(databaseStatements(stmts, "app", true)); len(got) != 2 || got[1] != 3 {
		t.Fatalf("one catalog: %v", got)
	}
}
