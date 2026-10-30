package sqlparse

import (
	"strings"
	"testing"
)

func TestNormalizeLexically(t *testing.T) {
	for in, want := range map[string]string{
		`SELECT * FROM users WHERE email = 'a@b.c' AND id = 42`:          `SELECT * FROM users WHERE email = $1 AND id = $2`,
		`select "Col1", t2.x from t2 where y > 1.5e3 -- 'secret'` + "\n": `select "Col1", t2.x from t2 where y > $1 ` + "\n",
		`SELECT E'it\'s', $$body 'x'$$, $f$ 1 $f$ FROM t WHERE a = $1`:   `SELECT $2, $3, $4 FROM t WHERE a = $1`,
		`INSERT INTO t VALUES ('it''s', x'ff') /* 99 */`:                 `INSERT INTO t VALUES ($1, $2) `,
	} {
		if got := NormalizeLexically(in); got != want {
			t.Errorf("%s\n got %s\nwant %s", in, got, want)
		}
	}
}

func TestNormalizeHidesConstants(t *testing.T) {
	n, fp := Normalize(`SELECT * FROM users WHERE email = 'alice@example.com'`)
	if strings.Contains(n, "alice") || fp == "" {
		t.Fatalf("%q %q", n, fp)
	}
	_, fp2 := Normalize(`SELECT * FROM users WHERE email = 'bob@example.com'`)
	if fp != fp2 {
		t.Fatalf("same statement, different constants: %q %q", fp, fp2)
	}
}
