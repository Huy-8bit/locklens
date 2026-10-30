package targetdb

import (
	"strings"
	"testing"
)

func TestRoleStatements(t *testing.T) {
	st := RoleStatements(`my"db`, "locklens_monitor", "locklens_reader", []string{"locklens_reader"}, "p'w", "secret")
	if len(st) != 6 {
		t.Fatalf("statements = %q", st)
	}
	want := []string{
		`CREATE ROLE "locklens_monitor" WITH LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION NOBYPASSRLS CONNECTION LIMIT 10 PASSWORD 'p''w'`,
		`GRANT pg_monitor TO "locklens_monitor"`,
		`ALTER ROLE "locklens_reader" WITH LOGIN`,
		`GRANT pg_read_all_data TO "locklens_reader"`,
		`ALTER ROLE "locklens_reader" SET default_transaction_read_only = on`,
		`GRANT CONNECT ON DATABASE "my""db" TO "locklens_monitor", "locklens_reader"`,
	}
	for i, w := range want {
		if !strings.HasPrefix(st[i], w) {
			t.Errorf("statement %d = %s\nwant prefix %s", i, st[i], w)
		}
	}
}
