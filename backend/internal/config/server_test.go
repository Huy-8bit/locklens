package config

import (
	"strings"
	"testing"
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
