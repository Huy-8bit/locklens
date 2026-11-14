package repository

import "testing"

func TestDSNSetsPoolMaxConns(t *testing.T) {
	cases := map[string]bool{
		"postgres://u:p@h:5432/db?sslmode=disable":                   false,
		"postgres://u:p@h:5432/db?sslmode=disable&pool_max_conns=32": true,
		"postgresql://h/db?pool_max_conns=4":                         true,
		"host=h dbname=db user=u":                                    false,
		"host=h dbname=db pool_max_conns=16 user=u":                  true,
		"host=h dbname=db application_name=pool_max_conns_tool":      false,
		"postgres://u:p@h/db?application_name=pool_max_conns%3D3":    false,
	}
	for dsn, want := range cases {
		if got := dsnSetsPoolMaxConns(dsn); got != want {
			t.Errorf("%q: got %v, want %v", dsn, got, want)
		}
	}
}
