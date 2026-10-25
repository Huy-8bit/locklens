package onboard

import (
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
)

func TestWithCredentials(t *testing.T) {
	cases := []struct{ in, wantHost, wantDB string }{
		{"postgres://postgres:secret@db.example:5433/shop?sslmode=require&application_name=x", "db.example", "shop"},
		{"postgresql://postgres@db.example/shop?user=other&password=leak", "db.example", "shop"},
		{"host=db.example port=5433 dbname=shop user=postgres password=secret sslmode=require", "db.example", "shop"},
		{"host='db.example' dbname = 'my shop' user='postgres' password='it\\'s secret'", "db.example", "my shop"},
	}
	for _, c := range cases {
		out, err := WithCredentials(c.in, "locklens_monitor", "p@ss w'rd\\x")
		if err != nil {
			t.Fatalf("%s: %v", c.in, err)
		}
		cfg, err := pgx.ParseConfig(out)
		if err != nil {
			t.Fatalf("%s -> %s: %v", c.in, out, err)
		}
		if cfg.User != "locklens_monitor" || cfg.Password != "p@ss w'rd\\x" || cfg.Host != c.wantHost || cfg.Database != c.wantDB {
			t.Errorf("%s -> user=%q password=%q host=%q db=%q", c.in, cfg.User, cfg.Password, cfg.Host, cfg.Database)
		}
		if strings.Contains(out, "secret") || strings.Contains(out, "leak") {
			t.Errorf("old credentials kept: %s", out)
		}
	}
	for _, bad := range []string{"just some text", "host='unterminated"} {
		if _, err := WithCredentials(bad, "u", "p"); err == nil {
			t.Errorf("%q: want an error", bad)
		}
	}
}
