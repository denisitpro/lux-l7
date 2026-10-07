package config_test

import (
	"strings"
	"testing"

	"github.com/denisitpro/lux-l7/internal/config"
)

func TestListenerTLSDefaultsOff(t *testing.T) {
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.HTTPTLSCertFile != "" || cfg.HTTPTLSKeyFile != "" || cfg.HTTPH2C {
		t.Fatalf("listener TLS and h2c must be off by default: %+v", cfg)
	}
}

func TestListenerTLSPairBothOrNeither(t *testing.T) {
	for _, tc := range []struct{ name, cert, key string }{
		{"cert only", "/x/tls.crt", ""},
		{"key only", "", "/x/tls.key"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("HTTP_TLS_CERT_FILE", tc.cert)
			t.Setenv("HTTP_TLS_KEY_FILE", tc.key)
			_, err := config.Load()
			if err == nil || !strings.Contains(err.Error(), "HTTP_TLS_CERT_FILE and HTTP_TLS_KEY_FILE must be set together") {
				t.Fatalf("want pair error, got %v", err)
			}
		})
	}

	t.Setenv("HTTP_TLS_CERT_FILE", " /x/tls.crt ")
	t.Setenv("HTTP_TLS_KEY_FILE", "/x/tls.key")
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.HTTPTLSCertFile != "/x/tls.crt" || cfg.HTTPTLSKeyFile != "/x/tls.key" {
		t.Fatalf("pair = %q %q", cfg.HTTPTLSCertFile, cfg.HTTPTLSKeyFile)
	}
}

func TestListenerH2CParsing(t *testing.T) {
	t.Setenv("HTTP_H2C", "true")
	cfg, err := config.Load()
	if err != nil || !cfg.HTTPH2C {
		t.Fatalf("h2c = %v err = %v", cfg.HTTPH2C, err)
	}
	t.Setenv("HTTP_H2C", "yes")
	if _, err := config.Load(); err == nil || !strings.Contains(err.Error(), "HTTP_H2C") {
		t.Fatalf("want HTTP_H2C error, got %v", err)
	}
}

func TestH2CWithTLSWarns(t *testing.T) {
	warned := func(c config.Config) bool {
		return strings.Contains(strings.Join(c.Warnings(), "|"), "HTTP_H2C is ignored")
	}
	if warned(config.Config{HTTPH2C: true}) {
		t.Fatal("h2c without TLS must not warn")
	}
	if warned(config.Config{HTTPTLSCertFile: "c", HTTPTLSKeyFile: "k"}) {
		t.Fatal("TLS without h2c must not warn")
	}
	if !warned(config.Config{HTTPH2C: true, HTTPTLSCertFile: "c", HTTPTLSKeyFile: "k"}) {
		t.Fatal("h2c together with TLS must warn")
	}
}
