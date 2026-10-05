package config_test

import (
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
