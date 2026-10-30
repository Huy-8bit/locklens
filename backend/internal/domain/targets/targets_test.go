package targets

import (
	"errors"
	"strings"
	"testing"
)

func TestValidateDefaultsAndDisplayFields(t *testing.T) {
	s := &service{}
	in := Input{Name: "  orders  ", DSN: "postgres://observer:cnrypw@db.internal:6432/orders?sslmode=disable", Environment: " Production "}
	tgt, err := s.validate(&in, true)
	if err != nil {
		t.Fatal(err)
	}
	if in.Name != "orders" || in.IntervalSeconds != 10 || in.MaxConnections != 2 || in.Environment != "production" {
		t.Errorf("defaults not applied: %+v", in)
	}
	if tgt.Host != "db.internal" || tgt.Port != 6432 || tgt.Database != "orders" || tgt.User != "observer" {
		t.Errorf("display fields = %+v", tgt)
	}
}

func TestValidateRejectsWithoutEchoingSecrets(t *testing.T) {
	s := &service{}
	empty, bad := "", "ftp://x/metrics"
	cases := []struct {
		in    Input
		field string
	}{
		{Input{Name: "", DSN: "postgres://h/db"}, "name"},
		{Input{Name: strings.Repeat("n", 101), DSN: "postgres://h/db"}, "name"},
		{Input{Name: "a\x1b[31m", DSN: "postgres://h/db"}, "name"},
		{Input{Name: "a", Description: strings.Repeat("d", 1001), DSN: "postgres://h/db"}, "description"},
		{Input{Name: "a", IntervalSeconds: 4, DSN: "postgres://h/db"}, "interval_seconds"},
		{Input{Name: "a", IntervalSeconds: 3601, DSN: "postgres://h/db"}, "interval_seconds"},
		{Input{Name: "a", MaxConnections: 6, DSN: "postgres://h/db"}, "max_connections"},
		{Input{Name: "a", NodeExporterURL: &bad, DSN: "postgres://h/db"}, "node_exporter_url"},
		{Input{Name: "a"}, "dsn"},
		{Input{Name: "a", DSN: "postgres://u:cnrysecret@h:notaport/db"}, "dsn"},
		{Input{Name: "a", DSN: "postgres://h/db", Environment: "Prod uction"}, "environment"},
		{Input{Name: "a", DSN: "postgres://h/db", Environment: "-prod"}, "environment"},
	}
	for _, c := range cases {
		_, err := s.validate(&c.in, true)
		var ve *ValidationError
		if !errors.As(err, &ve) || ve.Fields[c.field] == "" {
			t.Errorf("%+v: want a %s error, got %v", c.in, c.field, err)
			continue
		}
		for _, msg := range ve.Fields {
			if strings.Contains(msg, "cnry") {
				t.Errorf("validation message echoes the DSN: %q", msg)
			}
		}
	}
	// An empty node_exporter URL means "none".
	in := Input{Name: "a", DSN: "postgres://h/db", NodeExporterURL: &empty}
	if _, err := s.validate(&in, true); err != nil || in.NodeExporterURL != nil {
		t.Errorf("empty exporter URL: %v %v", err, in.NodeExporterURL)
	}
	// On update the DSN may be omitted to keep the stored one.
	if _, err := s.validate(&Input{Name: "a"}, false); err != nil {
		t.Errorf("update without DSN: %v", err)
	}
}
