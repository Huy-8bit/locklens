package config

import (
	"strings"
	"testing"
	"time"
)

func env(m map[string]string) func(string) string {
	base := map[string]string{"LOCKLENS_STORE_DSN": "postgres://x@db/locklens", "LOCKLENS_MASTER_KEY": "k"}
	for k, v := range m {
		base[k] = v
	}
	return func(k string) string { return base[k] }
}

func TestTrustedProxies(t *testing.T) {
	c, err := LoadConfig(env(map[string]string{"LOCKLENS_TRUSTED_PROXIES": " 172.16.0.0/12, 10.1.2.3 ,::1"}))
	if err != nil {
		t.Fatal(err)
	}
	got := []string{}
	for _, p := range c.TrustedProxies {
		got = append(got, p.String())
	}
	if strings.Join(got, ",") != "172.16.0.0/12,10.1.2.3/32,::1/128" {
		t.Fatalf("trusted proxies: %v", got)
	}
	if c, err := LoadConfig(env(nil)); err != nil || len(c.TrustedProxies) != 0 {
		t.Fatalf("default must trust no proxy: %v %v", c.TrustedProxies, err)
	}
	c, err = LoadConfig(env(map[string]string{"LOCKLENS_TRUSTED_PROXIES": "Frontend, proxy.example.internal"}))
	if err != nil || strings.Join(c.TrustedProxyHosts, ",") != "frontend,proxy.example.internal" || len(c.TrustedProxies) != 0 {
		t.Fatalf("host names: %v %v %v", c.TrustedProxyHosts, c.TrustedProxies, err)
	}
	if _, err := LoadConfig(env(map[string]string{"LOCKLENS_TRUSTED_PROXIES": "http://frontend"})); err == nil || !strings.Contains(err.Error(), "LOCKLENS_TRUSTED_PROXIES") {
		t.Fatalf("a URL must be rejected: %v", err)
	}
}

func TestStoreTimescale(t *testing.T) {
	get := func(kv map[string]string) func(string) string { return func(k string) string { return kv[k] } }
	st, err := LoadStoreTimescale(get(nil))
	if err != nil || st.Mode != "auto" || st.CompressAfter != 24*time.Hour {
		t.Fatalf("defaults: %+v %v", st, err)
	}
	st, err = LoadStoreTimescale(get(map[string]string{"LOCKLENS_STORE_TIMESCALE": "ON", "LOCKLENS_STORE_COMPRESS_AFTER": "0"}))
	if err != nil || st.Mode != "on" || st.CompressAfter != 0 {
		t.Fatalf("on without compression: %+v %v", st, err)
	}
	for _, bad := range []map[string]string{
		{"LOCKLENS_STORE_TIMESCALE": "yes"},
		{"LOCKLENS_STORE_COMPRESS_AFTER": "10m"},
		{"LOCKLENS_STORE_COMPRESS_AFTER": "soon"},
	} {
		if _, err := LoadStoreTimescale(get(bad)); err == nil {
			t.Errorf("%v accepted", bad)
		}
	}
}
