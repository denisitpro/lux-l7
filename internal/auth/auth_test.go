package auth_test

import (
	"testing"

	"github.com/denisitpro/lux-l7/internal/auth"
	"github.com/denisitpro/lux-l7/internal/config"
)

func TestInstanceToken(t *testing.T) {
	cfg := config.Config{
		AuthEnabled:   true,
		AuthMode:      config.AuthModeInstance,
		InstanceToken: "secret",
	}
	v, err := auth.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if !v.Authorize("secret", "api.example.com") {
		t.Fatal("expected valid token")
	}
	if v.Authorize("wrong", "api.example.com") {
		t.Fatal("expected invalid token")
	}
}
