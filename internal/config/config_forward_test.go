package config_test

import (
	"strings"
	"testing"

	"github.com/denisitpro/lux-l7/internal/config"
)

func TestForwardClientIPDefaults(t *testing.T) {
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.UpstreamForwardClientIP || len(cfg.UpstreamTrustedProxies) != 0 {
		t.Fatalf("want off and empty by default, got %v %v", cfg.UpstreamForwardClientIP, cfg.UpstreamTrustedProxies)
	}
}

func TestTrustedProxiesParse(t *testing.T) {
	t.Setenv("UPSTREAM_FORWARD_CLIENT_IP", "true")
	t.Setenv("UPSTREAM_TRUSTED_PROXIES", "10.0.0.0/8, 192.0.2.7, 2001:db8::/32, 2001:db8:1::5")
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.UpstreamForwardClientIP || len(cfg.UpstreamTrustedProxies) != 4 {
		t.Fatalf("got %v %v", cfg.UpstreamForwardClientIP, cfg.UpstreamTrustedProxies)
	}
	want := []string{"10.0.0.0/8", "192.0.2.7/32", "2001:db8::/32", "2001:db8:1::5/128"}
	for i, p := range cfg.UpstreamTrustedProxies {
		if p.String() != want[i] {
			t.Fatalf("entry %d = %s, want %s", i, p, want[i])
		}
	}
}

func TestForwardClientIPInvalid(t *testing.T) {
	for key, val := range map[string]string{
		"UPSTREAM_FORWARD_CLIENT_IP": "yes",
		"UPSTREAM_TRUSTED_PROXIES":   "10.0.0.0/33",
	} {
		t.Run(key, func(t *testing.T) {
			t.Setenv(key, val)
			if _, err := config.Load(); err == nil || !strings.Contains(err.Error(), key) {
				t.Fatalf("want %s error, got %v", key, err)
			}
		})
	}
	t.Run("not an IP", func(t *testing.T) {
		t.Setenv("UPSTREAM_TRUSTED_PROXIES", "10.0.0.0/8,example.com")
		if _, err := config.Load(); err == nil || !strings.Contains(err.Error(), "UPSTREAM_TRUSTED_PROXIES") {
			t.Fatalf("got %v", err)
		}
	})
}

func TestTrustedProxiesWithoutFlagWarns(t *testing.T) {
	t.Setenv("UPSTREAM_TRUSTED_PROXIES", "10.0.0.0/8")
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(cfg.Warnings(), "|"), "UPSTREAM_TRUSTED_PROXIES has no effect") {
		t.Fatalf("no warning: %v", cfg.Warnings())
	}
}
