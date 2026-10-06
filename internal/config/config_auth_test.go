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
	"TUNNEL_ADDR", "TUNNEL_ALLOW_PORTS", "TUNNEL_IDLE_TIMEOUT", "TUNNEL_MAX_CONNECTIONS",
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
		// AUTH_ENABLED / AUTH_MODE are removed: never parsed, never change the outcome.
		{"enabled=true without token stays off", map[string]string{"AUTH_ENABLED": "true"}, "", false, config.AuthModeInstance},
		{"enabled=false alone", map[string]string{"AUTH_ENABLED": "false"}, "", false, config.AuthModeInstance},
		{"enabled=garbage", map[string]string{"AUTH_ENABLED": "garbage"}, "", false, config.AuthModeInstance},
		{"enabled=false does not turn tokens off", map[string]string{"AUTH_ENABLED": "false", "PROXY_AUTH_TOKEN": "x"}, "", true, config.AuthModeInstance},
		{"mode=bogus", map[string]string{"AUTH_MODE": "bogus"}, "", false, config.AuthModeInstance},
		{"mode=per_host does not override instance token", map[string]string{"AUTH_MODE": "per_host", "PROXY_AUTH_TOKEN": "x"}, "", true, config.AuthModeInstance},
		{"mode=instance does not override host map", map[string]string{"AUTH_MODE": "instance", "PROXY_AUTH_HOST_MAP": hostMap}, "", true, config.AuthModePerHost},
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
	if _, err := config.Load(); err == nil || !strings.Contains(err.Error(), "PROXY_AUTH_TOKEN") {
		t.Fatalf("route mode + token: want startup error, got %v", err)
	}
	t.Setenv("PROXY_AUTH_TOKEN", "")
	t.Setenv("PROXY_AUTH_HOST_MAP", hostMap)
	if _, err := config.Load(); err == nil || !strings.Contains(err.Error(), "PROXY_AUTH_HOST_MAP") {
		t.Fatalf("route mode + host map: want startup error, got %v", err)
	}
	t.Setenv("PROXY_AUTH_HOST_MAP", "")
	if cfg, err := config.Load(); err != nil || cfg.AuthEnabled {
		t.Fatalf("route mode without tokens must load: %v", err)
	}
	t.Setenv("AUTH_ENABLED", "true") // removed variable: ignored, not a route-mode error
	if cfg, err := config.Load(); err != nil || cfg.AuthEnabled {
		t.Fatalf("route mode with ignored AUTH_ENABLED must load with auth off: %v", err)
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

func TestRemovedAuthVarsWarn(t *testing.T) {
	warnings := func(t *testing.T, env map[string]string) []string {
		t.Helper()
		clearEnv(t)
		for k, v := range env {
			t.Setenv(k, v)
		}
		cfg, err := config.Load()
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, w := range cfg.Warnings() {
			if strings.Contains(w, "is ignored (removed)") {
				out = append(out, w)
			}
		}
		return out
	}
	if got := warnings(t, nil); len(got) != 0 {
		t.Fatalf("no removed vars set, got warnings %v", got)
	}
	const tail = ", it follows PROXY_AUTH_TOKEN / PROXY_AUTH_HOST_MAP"
	cases := []struct {
		name string
		env  map[string]string
		want []string
	}{
		{"enabled=true no token", map[string]string{"AUTH_ENABLED": "true"},
			[]string{"AUTH_ENABLED is ignored (removed): auth is off" + tail}},
		{"enabled=false alone", map[string]string{"AUTH_ENABLED": "false"},
			[]string{"AUTH_ENABLED is ignored (removed): auth is off" + tail}},
		{"enabled=garbage", map[string]string{"AUTH_ENABLED": "garbage"},
			[]string{"AUTH_ENABLED is ignored (removed): auth is off" + tail}},
		{"mode with instance token", map[string]string{"AUTH_MODE": "instance", "PROXY_AUTH_TOKEN": "x"},
			[]string{"AUTH_MODE is ignored (removed): auth is on (instance)" + tail}},
		{"both with host map", map[string]string{"AUTH_ENABLED": "true", "AUTH_MODE": "per_host", "PROXY_AUTH_HOST_MAP": hostMap},
			[]string{"AUTH_ENABLED is ignored (removed): auth is on (per_host)" + tail, "AUTH_MODE is ignored (removed): auth is on (per_host)" + tail}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := warnings(t, tc.env)
			if strings.Join(got, "|") != strings.Join(tc.want, "|") {
				t.Fatalf("warnings = %q, want %q", got, tc.want)
			}
		})
	}
}
