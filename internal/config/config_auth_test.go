package config_test

import (
	"os"
	"strings"
	"testing"

	"github.com/denisitpro/lux-l7/internal/config"
)

// configEnvKeys is every variable config.Load reads; clearEnv blanks them so a test
// does not depend on the machine's environment.
var configEnvKeys = []string{
	"PROXY_MODE", "ROUTES", "ROUTE_MAX_HOPS", "HTTP_ADDR", "LOG_LEVEL",
	"HTTP_READ_HEADER_TIMEOUT", "HTTP_READ_TIMEOUT", "HTTP_WRITE_TIMEOUT", "HTTP_IDLE_TIMEOUT",
	"SHUTDOWN_TIMEOUT", "UPSTREAM_TIMEOUT", "UPSTREAM_MAX_BYTES", "UPSTREAM_DEFAULT_SCHEME",
	"UPSTREAM_ALLOW_HTTP", "UPSTREAM_ALLOW_PRIVATE", "UPSTREAM_ALLOW_PORTS",
	"UPSTREAM_ALLOW_HOSTS", "UPSTREAM_ALLOW_SUFFIXES", "UPSTREAM_DENY_HOSTS",
	"AUTH_ENABLED", "AUTH_MODE", "PROXY_AUTH_TOKEN", "PROXY_AUTH_HOST_MAP",
	"TUNNEL_ADDR", "TUNNEL_ALLOW_PORTS", "TUNNEL_IDLE_TIMEOUT",
	"TUNNEL_TLS_CERT_FILE", "TUNNEL_TLS_KEY_FILE",
}

func clearEnv(t *testing.T) {
	t.Helper()
	for _, k := range configEnvKeys {
		t.Setenv(k, "") // registers restore; Load treats empty as unset
		if err := os.Unsetenv(k); err != nil {
			t.Fatal(err)
		}
	}
}

const hostMap = `{"api.example.com":"tok"}`

func TestEmptyEnvironmentLoads(t *testing.T) {
	clearEnv(t)
	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("empty environment must load: %v", err)
	}
	if cfg.ProxyMode != config.ProxyModeHeader {
		t.Fatalf("mode = %q, want header", cfg.ProxyMode)
	}
	if cfg.AuthEnabled || cfg.TunnelAddr != "" || cfg.HTTPAddr != ":8080" {
		t.Fatalf("unexpected defaults: %+v", cfg)
	}
	if got := cfg.Warnings(); len(got) != 1 || !strings.Contains(got[0], "allow-list empty") {
		t.Fatalf("warnings = %v", got)
	}
}

func TestAuthResolution(t *testing.T) {
	cases := []struct {
		name     string
		env      map[string]string
		wantErr  string // empty: must load
		wantAuth bool
		wantMode config.AuthMode
	}{
		// Inference from tokens.
		{"no env", nil, "", false, config.AuthModeInstance},
		{"instance token only", map[string]string{"PROXY_AUTH_TOKEN": "x"}, "", true, config.AuthModeInstance},
		{"host map only", map[string]string{"PROXY_AUTH_HOST_MAP": hostMap}, "", true, config.AuthModePerHost},
		{"both tokens", map[string]string{"PROXY_AUTH_TOKEN": "x", "PROXY_AUTH_HOST_MAP": hostMap}, "choose one", false, ""},
		// AUTH_ENABLED=true.
		{"true no tokens", map[string]string{"AUTH_ENABLED": "true"}, "requires PROXY_AUTH_TOKEN", false, ""},
		{"true instance", map[string]string{"AUTH_ENABLED": "true", "PROXY_AUTH_TOKEN": "x"}, "", true, config.AuthModeInstance},
		{"true host map", map[string]string{"AUTH_ENABLED": "true", "PROXY_AUTH_HOST_MAP": hostMap}, "", true, config.AuthModePerHost},
		// AUTH_ENABLED=false.
		{"false no tokens", map[string]string{"AUTH_ENABLED": "false"}, "", false, config.AuthModeInstance},
		{"false instance", map[string]string{"AUTH_ENABLED": "false", "PROXY_AUTH_TOKEN": "x"}, "contradicts", false, ""},
		{"false host map", map[string]string{"AUTH_ENABLED": "false", "PROXY_AUTH_HOST_MAP": hostMap}, "contradicts", false, ""},
		// AUTH_MODE consistency.
		{"mode instance + instance token", map[string]string{"AUTH_MODE": "instance", "PROXY_AUTH_TOKEN": "x"}, "", true, config.AuthModeInstance},
		{"mode per_host + host map", map[string]string{"AUTH_MODE": "per_host", "PROXY_AUTH_HOST_MAP": hostMap}, "", true, config.AuthModePerHost},
		{"mode per_host + instance token", map[string]string{"AUTH_MODE": "per_host", "PROXY_AUTH_TOKEN": "x"}, "AUTH_MODE=per_host", false, ""},
		{"mode instance + host map", map[string]string{"AUTH_MODE": "instance", "PROXY_AUTH_HOST_MAP": hostMap}, "AUTH_MODE=instance", false, ""},
		{"enabled + per_host + instance token", map[string]string{"AUTH_ENABLED": "true", "AUTH_MODE": "per_host", "PROXY_AUTH_TOKEN": "x"}, "AUTH_MODE=per_host", false, ""},
		{"bad mode", map[string]string{"AUTH_MODE": "bogus"}, "AUTH_MODE must be", false, ""},
		// Backward compatibility: existing deployments behave as before.
		{"compat: only AUTH_ENABLED=false", map[string]string{"AUTH_ENABLED": "false"}, "", false, config.AuthModeInstance},
		{"compat: enabled+instance mode+token", map[string]string{"AUTH_ENABLED": "true", "AUTH_MODE": "instance", "PROXY_AUTH_TOKEN": "x"}, "", true, config.AuthModeInstance},
		{"compat: enabled+per_host+map", map[string]string{"AUTH_ENABLED": "true", "AUTH_MODE": "per_host", "PROXY_AUTH_HOST_MAP": hostMap}, "", true, config.AuthModePerHost},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			clearEnv(t)
			for k, v := range tc.env {
				t.Setenv(k, v)
			}
			cfg, err := config.Load()
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("want error containing %q, got %v", tc.wantErr, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if cfg.AuthEnabled != tc.wantAuth || cfg.AuthMode != tc.wantMode {
				t.Fatalf("auth=%v mode=%q, want auth=%v mode=%q", cfg.AuthEnabled, cfg.AuthMode, tc.wantAuth, tc.wantMode)
			}
		})
	}
}

func TestRouteModeAuth(t *testing.T) {
	routes := `[{"host":"example.com","path_prefix":"/","upstream":"https://origin.example.com"}]`
	clearEnv(t)
	t.Setenv("PROXY_MODE", "route")
	t.Setenv("ROUTES", routes)
	t.Setenv("PROXY_AUTH_TOKEN", "x")
	cfg, err := config.Load()
	if err != nil || cfg.AuthEnabled {
		t.Fatalf("route mode ignores tokens: auth=%v err=%v", cfg.AuthEnabled, err)
	}
	t.Setenv("AUTH_ENABLED", "true")
	if _, err := config.Load(); err == nil || !strings.Contains(err.Error(), "route mode") {
		t.Fatalf("want route-mode error, got %v", err)
	}
}

func TestTunnelFollowsEffectiveAuth(t *testing.T) {
	clearEnv(t)
	t.Setenv("TUNNEL_ADDR", ":8443")
	if _, err := config.Load(); err == nil || !strings.Contains(err.Error(), "open CONNECT relay") {
		t.Fatalf("no auth, no allow-list: want open-relay error, got %v", err)
	}
	t.Setenv("PROXY_AUTH_TOKEN", "x") // two variables, nothing else
	cfg, err := config.Load()
	if err != nil || !cfg.AuthEnabled {
		t.Fatalf("tunnel + token must load with auth on: %v", err)
	}
}

func TestLegacyAuthVarsWarn(t *testing.T) {
	clearEnv(t)
	cfg, err := config.Load()
	if err != nil || len(cfg.LegacyAuthVars) != 0 {
		t.Fatalf("legacy = %v err=%v", cfg.LegacyAuthVars, err)
	}
	t.Setenv("AUTH_ENABLED", "false")
	t.Setenv("AUTH_MODE", "instance")
	cfg, err = config.Load()
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, w := range cfg.Warnings() {
		if strings.Contains(w, "AUTH_ENABLED and AUTH_MODE set: legacy") {
			found = true
		}
	}
	if !found {
		t.Fatalf("warnings = %v", cfg.Warnings())
	}
}
