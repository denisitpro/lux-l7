package config_test

import (
	"strings"
	"testing"

	"github.com/denisitpro/lux-l7/internal/config"
)

func TestLoadDefaults(t *testing.T) {
	t.Setenv("HTTP_ADDR", ":9090")
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.HTTPAddr != ":9090" {
		t.Fatalf("HTTPAddr = %q", cfg.HTTPAddr)
	}
	if cfg.AuthEnabled {
		t.Fatal("expected auth disabled by default")
	}
}

func TestUnparsableBoolFailsLoad(t *testing.T) {
	for _, key := range []string{"AUTH_ENABLED", "UPSTREAM_ALLOW_HTTP"} {
		t.Run(key, func(t *testing.T) {
			t.Setenv(key, "yes")
			_, err := config.Load()
			if err == nil || !strings.Contains(err.Error(), key) {
				t.Fatalf("expected %s error, got %v", key, err)
			}
		})
	}
}

func TestNonPositiveLimitsFailLoad(t *testing.T) {
	for key, val := range map[string]string{
		"UPSTREAM_MAX_BYTES": "0",
		"UPSTREAM_TIMEOUT":   "0s",
		"SHUTDOWN_TIMEOUT":   "-1s",
	} {
		t.Run(key, func(t *testing.T) {
			t.Setenv(key, val)
			_, err := config.Load()
			if err == nil || !strings.Contains(err.Error(), key) {
				t.Fatalf("expected %s error, got %v", key, err)
			}
		})
	}
	t.Run("UPSTREAM_MAX_BYTES negative", func(t *testing.T) {
		t.Setenv("UPSTREAM_MAX_BYTES", "-5")
		if _, err := config.Load(); err == nil {
			t.Fatal("expected error")
		}
	})
}

func TestPolicyEntriesWithPortFailLoad(t *testing.T) {
	for _, key := range []string{"UPSTREAM_ALLOW_HOSTS", "UPSTREAM_ALLOW_SUFFIXES", "UPSTREAM_DENY_HOSTS"} {
		t.Run(key, func(t *testing.T) {
			t.Setenv(key, "api.example.com:8443")
			_, err := config.Load()
			if err == nil || !strings.Contains(err.Error(), key) {
				t.Fatalf("expected %s error, got %v", key, err)
			}
		})
	}
}

func TestDefaultSchemeHTTPNeedsAllowHTTP(t *testing.T) {
	t.Setenv("UPSTREAM_DEFAULT_SCHEME", "http")
	if _, err := config.Load(); err == nil || !strings.Contains(err.Error(), "UPSTREAM_ALLOW_HTTP") {
		t.Fatalf("expected UPSTREAM_ALLOW_HTTP error, got %v", err)
	}
	t.Setenv("UPSTREAM_ALLOW_HTTP", "true")
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.UpstreamAllowHTTP || cfg.UpstreamScheme != "http" {
		t.Fatalf("cfg = %+v", cfg)
	}
}
