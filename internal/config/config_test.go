package config

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestResolveDSNPrecedence(t *testing.T) {
	t.Setenv(EnvDSN, "postgres://env@localhost/db")
	got, err := ResolveDSN("postgres://flag@localhost/db")
	if err != nil || got != "postgres://flag@localhost/db" {
		t.Fatalf("flag must win: %q %v", got, err)
	}
	got, err = ResolveDSN("")
	if err != nil || got != "postgres://env@localhost/db" {
		t.Fatalf("env fallback: %q %v", got, err)
	}
	t.Setenv(EnvDSN, "")
	if _, err := ResolveDSN("  "); !errors.Is(err, ErrNoDSN) {
		t.Fatalf("expected ErrNoDSN, got %v", err)
	}
}

func TestPoolConfigSetsObserverParameters(t *testing.T) {
	cfg, _, err := PoolConfig("postgres://u:secret@db.example:6543/app?sslmode=require",
		Options{ConnectTimeout: 3 * time.Second, QueryTimeout: 7 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	rp := cfg.ConnConfig.RuntimeParams
	if rp["application_name"] != "locklens" || rp["default_transaction_read_only"] != "on" || rp["statement_timeout"] != "7000" {
		t.Errorf("runtime params = %v", rp)
	}
	if cfg.ConnConfig.TLSConfig == nil {
		t.Error("sslmode=require must keep TLS enabled")
	}
	if cfg.ConnConfig.ConnectTimeout != 3*time.Second || cfg.MaxConns != 2 {
		t.Errorf("connect timeout %v, max conns %d", cfg.ConnConfig.ConnectTimeout, cfg.MaxConns)
	}
	tgt := TargetOf(cfg)
	if tgt.String() != "db.example:6543/app as u" || strings.Contains(fmt.Sprint(tgt), "secret") {
		t.Errorf("target = %q", tgt)
	}
}

func TestInvalidDSNNeverEchoesInput(t *testing.T) {
	inputs := []string{
		"postgres://user:p@ss w0rd!@host:abc/db",
		"host=localhost password='unterminated",
		"postgres://user:Sup3rSecret@host:99999999/db",
		"postgres://user:Sup3rSecret@[::1/db",
	}
	for _, dsn := range inputs {
		_, _, err := PoolConfig(dsn, Options{})
		if err == nil {
			t.Errorf("expected error for %q", dsn)
			continue
		}
		var inv *InvalidDSNError
		if !errors.As(err, &inv) {
			t.Errorf("expected InvalidDSNError, got %T", err)
		}
		for _, leak := range []string{"p@ss", "w0rd", "unterminated", "Sup3rSecret", "user"} {
			if strings.Contains(err.Error(), leak) {
				t.Errorf("error for %q leaks %q: %s", dsn, leak, err)
			}
		}
	}
}

func TestRedactorRemovesSecretsAndEncodings(t *testing.T) {
	dsn := "postgres://app:s3cr%40t%20pw@localhost/db"
	r := NewRedactor(dsn)
	r.Add("s3cr@t pw")
	msgs := []string{
		"failed: " + dsn,
		"password s3cr@t pw rejected",
		"echo s3cr%40t%20pw",
		"echo s3cr%40t+pw",
	}
	for _, m := range msgs {
		got := r.String(m)
		if strings.Contains(got, "s3cr") {
			t.Errorf("String(%q) = %q still leaks", m, got)
		}
	}
	err := r.Error(fmt.Errorf("wrapped: %w", errors.New("auth s3cr@t pw")))
	if strings.Contains(err.Error(), "s3cr") {
		t.Errorf("Error leaks: %v", err)
	}
}

func TestRedactorKeyValueDSN(t *testing.T) {
	dsn := "host=localhost user=app password=hunter2xyz dbname=db"
	cfg, r, err := PoolConfig(dsn, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ConnConfig.Password != "hunter2xyz" {
		t.Fatal("password not parsed")
	}
	if got := r.String("oops hunter2xyz"); strings.Contains(got, "hunter2xyz") {
		t.Errorf("got %q", got)
	}
}

func TestRedactorPasswordFromEnvironment(t *testing.T) {
	t.Setenv("PGPASSWORD", "fromEnvSecret")
	_, r, err := PoolConfig("postgres://app@localhost/db", Options{})
	if err != nil {
		t.Fatal(err)
	}
	if got := r.String("x fromEnvSecret y"); strings.Contains(got, "fromEnvSecret") {
		t.Errorf("got %q", got)
	}
}

func TestRedactorErrorKeepsChain(t *testing.T) {
	base := errors.New("boom")
	err := NewRedactor("postgres://a:b@c/d").Error(fmt.Errorf("x: %w", base))
	if !errors.Is(err, base) {
		t.Error("errors.Is must see the wrapped error")
	}
}
