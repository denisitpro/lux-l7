package config_test

import (
	"strings"
	"testing"

	"github.com/denisitpro/lux-l7/internal/config"
)

func TestPathModeLoad(t *testing.T) {
	t.Setenv("PROXY_MODE", "Path")
	t.Setenv("PROXY_AUTH_TOKEN", "tok")
	t.Setenv("UPSTREAM_ALLOW_HOSTS", "api.example.com")
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ProxyMode != config.ProxyModePath || !cfg.AuthEnabled {
		t.Fatalf("mode=%q auth=%v", cfg.ProxyMode, cfg.AuthEnabled)
	}
}

func TestPathModeRejectsRoutes(t *testing.T) {
	t.Setenv("PROXY_MODE", "path")
	t.Setenv("ROUTES", `[{"host":"example.com","path_prefix":"/","upstream":"http://127.0.0.1"}]`)
	_, err := config.Load()
	if err == nil || !strings.Contains(err.Error(), "ROUTES must not be set when PROXY_MODE=path") {
		t.Fatalf("got %v", err)
	}
}

func TestPathModeKeepsHeaderModePolicyChecks(t *testing.T) {
	t.Setenv("PROXY_MODE", "path")
	t.Setenv("UPSTREAM_DEFAULT_SCHEME", "http")
	if _, err := config.Load(); err == nil || !strings.Contains(err.Error(), "UPSTREAM_ALLOW_HTTP") {
		t.Fatalf("scheme gate: %v", err)
	}
	t.Setenv("UPSTREAM_DEFAULT_SCHEME", "https")
	t.Setenv("UPSTREAM_ALLOW_HOSTS", "api.example.com:443")
	if _, err := config.Load(); err == nil || !strings.Contains(err.Error(), "bare hostname") {
		t.Fatalf("allow entry with port: %v", err)
	}
}

func TestPathModeAllowsTunnelAndWarnsOpenAllowList(t *testing.T) {
	t.Setenv("PROXY_MODE", "path")
	t.Setenv("TUNNEL_ADDR", ":8081")
	t.Setenv("PROXY_AUTH_TOKEN", "tok")
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	var warned bool
	for _, w := range cfg.Warnings() {
		warned = warned || strings.Contains(w, "allow-list empty")
	}
	if !warned {
		t.Fatalf("no open allow-list warning: %v", cfg.Warnings())
	}
}
