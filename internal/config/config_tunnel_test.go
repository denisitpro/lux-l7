package config_test

import (
	"strings"
	"testing"
	"time"

	"github.com/denisitpro/lux-l7/internal/config"
)

func TestTunnelDefaults(t *testing.T) {
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.TunnelAddr != "" {
		t.Fatalf("tunnel must be off by default, got %q", cfg.TunnelAddr)
	}
	if got := cfg.TunnelAllowPorts; len(got) != 1 || got[0] != 443 {
		t.Fatalf("default tunnel ports = %v", got)
	}
	if cfg.TunnelIdleTimeout != 10*time.Minute {
		t.Fatalf("idle = %s", cfg.TunnelIdleTimeout)
	}
}

func TestTunnelConfigValidation(t *testing.T) {
	cases := []struct {
		name string
		env  map[string]string
		want string // empty: must load
	}{
		{"enabled with allow-list", map[string]string{"TUNNEL_ADDR": ":8443", "UPSTREAM_ALLOW_SUFFIXES": "example.com"}, ""},
		{"enabled with auth, empty allow-list", map[string]string{"TUNNEL_ADDR": ":8443", "AUTH_ENABLED": "true", "PROXY_AUTH_TOKEN": "t"}, ""},
		{"open relay", map[string]string{"TUNNEL_ADDR": ":8443"}, "open CONNECT relay"},
		{"deny list alone is not a policy", map[string]string{"TUNNEL_ADDR": ":8443", "UPSTREAM_DENY_HOSTS": "x.example.com"}, "open CONNECT relay"},
		{"route mode", map[string]string{"TUNNEL_ADDR": ":8443", "PROXY_MODE": "route", "ROUTES": `[{"host":"example.com","path_prefix":"/","upstream":"https://origin.example.com"}]`}, "PROXY_MODE=route"},
		{"cert without key", map[string]string{"TUNNEL_TLS_CERT_FILE": "/tls.crt"}, "must be set together"},
		{"key without cert", map[string]string{"TUNNEL_TLS_KEY_FILE": "/tls.key"}, "must be set together"},
		{"both tls files", map[string]string{"TUNNEL_TLS_CERT_FILE": "/tls.crt", "TUNNEL_TLS_KEY_FILE": "/tls.key"}, ""},
		{"bad port", map[string]string{"TUNNEL_ALLOW_PORTS": "443,x"}, "TUNNEL_ALLOW_PORTS"},
		{"zero port", map[string]string{"TUNNEL_ALLOW_PORTS": "0"}, "TUNNEL_ALLOW_PORTS"},
		{"bad idle", map[string]string{"TUNNEL_IDLE_TIMEOUT": "soon"}, "TUNNEL_IDLE_TIMEOUT"},
		{"zero idle", map[string]string{"TUNNEL_IDLE_TIMEOUT": "0s"}, "TUNNEL_IDLE_TIMEOUT"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for k, v := range tc.env {
				t.Setenv(k, v)
			}
			_, err := config.Load()
			switch {
			case tc.want == "" && err != nil:
				t.Fatalf("unexpected error: %v", err)
			case tc.want != "" && (err == nil || !strings.Contains(err.Error(), tc.want)):
				t.Fatalf("want error containing %q, got %v", tc.want, err)
			}
		})
	}
}

func TestTunnelPortsAndIdleParsed(t *testing.T) {
	t.Setenv("TUNNEL_ALLOW_PORTS", "443, 8443,9443")
	t.Setenv("TUNNEL_IDLE_TIMEOUT", "90s")
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.TunnelAllowPorts; len(got) != 3 || got[2] != 9443 {
		t.Fatalf("ports = %v", got)
	}
	if cfg.TunnelIdleTimeout != 90*time.Second {
		t.Fatalf("idle = %s", cfg.TunnelIdleTimeout)
	}
}
