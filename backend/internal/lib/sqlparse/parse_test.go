//go:build cgo

package sqlparse

import (
	"slices"
	"testing"
)

func codes(a Analysis) []string {
	var out []string
	for _, l := range a.Lints {
		out = append(out, l.Code)
	}
	slices.Sort(out)
	return out
}

func TestPredicatesAndOrder(t *testing.T) {
	a, err := Analyze(`SELECT o.id, o.total FROM shop.orders o JOIN shop.customers c ON c.id = o.customer_id
WHERE o.status = $1 AND o.created_at >= $2 AND c.country IN ($3, $4) AND o.deleted_at IS NULL ORDER BY o.created_at DESC LIMIT $5`)
	if err != nil {
		t.Fatal(err)
	}
	if a.Command != "select" || !a.HasLimit || a.Fingerprint == "" || len(a.Tables) != 2 || a.Tables[0].Qualified() != "shop.orders" {
		t.Fatalf("analysis = %+v", a)
	}
	want := []Predicate{
		{Table: "shop.customers", Column: "id", Op: "JOIN"},
		{Table: "shop.orders", Column: "customer_id", Op: "JOIN"},
		{Table: "shop.orders", Column: "status", Op: "="},
		{Table: "shop.orders", Column: "created_at", Op: ">="},
		{Table: "shop.customers", Column: "country", Op: "IN"},
		{Table: "shop.orders", Column: "deleted_at", Op: "IS NULL"},
	}
	if !slices.Equal(a.Predicates, want) {
		t.Fatalf("predicates = %+v", a.Predicates)
	}
	if len(a.OrderBy) != 1 || a.OrderBy[0] != (OrderColumn{Table: "shop.orders", Column: "created_at", Desc: true}) {
		t.Fatalf("order = %+v", a.OrderBy)
	}
	if len(a.Lints) != 0 {
		t.Fatalf("lints = %+v", a.Lints)
	}
	// A reversed comparison ($1 < col) is the same predicate as col > $1.
	b, _ := Analyze("SELECT 1 FROM t WHERE $1 < amount")
	if b.Predicates[0] != (Predicate{Table: "t", Column: "amount", Op: ">"}) {
		t.Fatalf("flipped = %+v", b.Predicates)
	}
}

func TestLints(t *testing.T) {
	cases := map[string][]string{
		"SELECT * FROM t WHERE lower(email) = $1":               {LintFunctionOnColumn, LintSelectStar},
		"SELECT id FROM t ORDER BY id LIMIT $1 OFFSET $2":       {LintOffset},
		"SELECT id FROM t WHERE name LIKE '%abc'":               {LintLeadingWildcard},
		"SELECT id FROM t WHERE id NOT IN (SELECT t_id FROM u)": {LintNotInSubquery},
		"SELECT id FROM t WHERE a = $1 OR b = $2":               {LintOrAcrossColumns},
		"DELETE FROM t":                                                  {LintNoWhere},
		"UPDATE t SET a = $1":                                            {LintNoWhere},
		"SELECT t.id FROM t, u":                                          {LintCartesian},
		"SELECT 1; SELECT 2":                                             {LintMultiStatement},
		"SELECT id FROM t WHERE a = $1 OR a = $2":                        nil,
		"UPDATE t SET a = $1 WHERE id = $2":                              nil,
		"SELECT id FROM t WHERE x NOT IN ($1, $2)":                       nil,
		"INSERT INTO t (a) VALUES ($1)":                                  nil,
		"WITH r AS (SELECT id FROM t WHERE a = $1) SELECT id FROM r":     nil,
		"SELECT id FROM t WHERE id IN (SELECT t_id FROM u WHERE x = $1)": nil,
	}
	for sql, want := range cases {
		a, err := Analyze(sql)
		if err != nil {
			t.Fatalf("%s: %v", sql, err)
		}
		if got := codes(a); !slices.Equal(got, want) {
			t.Errorf("%s: lints %v, want %v", sql, got, want)
		}
	}
	// The wrapped column is still a predicate, marked as wrapped.
	a, _ := Analyze("SELECT id FROM users WHERE lower(email) = $1")
	if a.Predicates[0] != (Predicate{Table: "users", Column: "email", Op: "=", Wrapped: "lower"}) {
		t.Fatalf("wrapped = %+v", a.Predicates)
	}
	// Predicates of subqueries and CTEs are collected with their tables.
	b, _ := Analyze("WITH r AS (SELECT id FROM t WHERE a = $1) SELECT id FROM r JOIN u ON u.r_id = r.id WHERE u.b > $2")
	if !slices.Contains(b.Predicates, Predicate{Table: "t", Column: "a", Op: "="}) || !slices.Contains(b.Predicates, Predicate{Table: "u", Column: "b", Op: ">"}) {
		t.Fatalf("cte = %+v", b.Predicates)
	}
}

func TestParseError(t *testing.T) {
	if _, err := Analyze("SELEC id FROM"); err == nil {
		t.Fatal("invalid SQL must fail")
	}
	if !Available() {
		t.Fatal("cgo build must have the parser")
	}
}
