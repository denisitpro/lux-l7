package config_test

import (
	"strings"
	"testing"

	"github.com/denisitpro/lux-l7/internal/config"
)

func TestServerTimeoutsMustBePositive(t *testing.T) {
	for _, key := range []string{"HTTP_READ_HEADER_TIMEOUT", "HTTP_IDLE_TIMEOUT"} {
		for _, val := range []string{"0", "0s", "-1s"} {
			t.Run(key+"="+val, func(t *testing.T) {
				t.Setenv(key, val)
				if _, err := config.Load(); err == nil || !strings.Contains(err.Error(), key) {
					t.Fatalf("expected %s error, got %v", key, err)
				}
			})
		}
	}
}

func TestModeSpecificVariableErrorsSayEveryMode(t *testing.T) {
	for key, val := range map[string]string{
		"UPSTREAM_ALLOW_PORTS":    "x",
		"TUNNEL_ALLOW_PORTS":      "x",
		"UPSTREAM_ALLOW_HTTP":     "yes",
		"UPSTREAM_ALLOW_PRIVATE":  "yes",
		"UPSTREAM_DEFAULT_SCHEME": "ftp",
		"ROUTE_MAX_HOPS":          "0",
	} {
		t.Run(key, func(t *testing.T) {
			t.Setenv(key, val)
			_, err := config.Load()
			if err == nil || !strings.Contains(err.Error(), key) || !strings.Contains(err.Error(), "checked in every PROXY_MODE") {
				t.Fatalf("want %s error naming every-mode check, got %v", key, err)
			}
		})
	}
}

func TestPrivateUpstreamWarning(t *testing.T) {
	warned := func(c config.Config) bool {
		return strings.Contains(strings.Join(c.Warnings(), "|"), "UPSTREAM_ALLOW_PRIVATE=true")
	}
	cases := []struct {
		name string
		cfg  config.Config
		want bool
	}{
		{"guard on", config.Config{ProxyMode: config.ProxyModeHeader}, false},
		{"header mode", config.Config{ProxyMode: config.ProxyModeHeader, UpstreamAllowPrivate: true}, true},
		{"header mode with tunnel", config.Config{ProxyMode: config.ProxyModeHeader, UpstreamAllowPrivate: true, TunnelAddr: ":8443"}, true},
		{"tunnel only", config.Config{ProxyMode: config.ProxyModeRoute, UpstreamAllowPrivate: true, TunnelAddr: ":8443"}, true},
		{"route mode, no tunnel", config.Config{ProxyMode: config.ProxyModeRoute, UpstreamAllowPrivate: true}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := warned(tc.cfg); got != tc.want {
				t.Fatalf("warned = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestListenerAddressClash(t *testing.T) {
	cases := []struct {
		name         string
		http, tunnel string
		clash        bool
	}{
		{"identical", ":8080", ":8080", true},
		{"wildcard spellings", ":8080", "0.0.0.0:8080", true},
		{"ipv6 wildcard", "[::]:8080", ":8080", true},
		{"wildcard vs specific host", ":8080", "127.0.0.1:8080", true},
		{"same specific host", "127.0.0.1:8080", "127.0.0.1:8080", true},
		{"different ports", ":8080", ":8443", false},
		{"different specific hosts", "127.0.0.1:8080", "127.0.0.2:8080", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("HTTP_ADDR", tc.http)
			t.Setenv("TUNNEL_ADDR", tc.tunnel)
			t.Setenv("UPSTREAM_ALLOW_SUFFIXES", "example.com")
			_, err := config.Load()
			if tc.clash && (err == nil || !strings.Contains(err.Error(), "TUNNEL_ADDR") || !strings.Contains(err.Error(), "HTTP_ADDR")) {
				t.Fatalf("want clash error, got %v", err)
			}
			if !tc.clash && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}
