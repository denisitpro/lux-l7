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

func instanceValidator(t *testing.T, tokens string) *auth.Validator {
	t.Helper()
	v, err := auth.New(config.Config{
		AuthEnabled:   true,
		AuthMode:      config.AuthModeInstance,
		InstanceToken: tokens,
	})
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestInstanceMultipleTokens(t *testing.T) {
	v := instanceValidator(t, "tok-a,tok-b,tok-c")
	for _, tok := range []string{"tok-a", "tok-b", "tok-c"} {
		if !v.Authorize(tok, "api.example.com") {
			t.Fatalf("token %q should be accepted", tok)
		}
	}
	for _, tok := range []string{"tok-d", "tok-a,tok-b", "tok-", ""} {
		if v.Authorize(tok, "api.example.com") {
			t.Fatalf("token %q should be rejected", tok)
		}
	}
	if got := v.InstanceTokenCount(); got != 3 {
		t.Fatalf("count = %d, want 3", got)
	}
}

func TestInstanceTokensWhitespaceAndEmptyItems(t *testing.T) {
	v := instanceValidator(t, "  tok-a , ,tok-b,, \t tok-a ,")
	if got := v.InstanceTokenCount(); got != 2 {
		t.Fatalf("count = %d, want 2 (trimmed, empties dropped, duplicates collapsed)", got)
	}
	for _, tok := range []string{"tok-a", "tok-b"} {
		if !v.Authorize(tok, "api.example.com") {
			t.Fatalf("token %q should be accepted", tok)
		}
	}
	if v.Authorize(" ", "api.example.com") {
		t.Fatal("blank token must not match an empty item")
	}
}

func TestInstanceTokenRevocation(t *testing.T) {
	before := instanceValidator(t, "tok-a,tok-b")
	if !before.Authorize("tok-b", "api.example.com") {
		t.Fatal("tok-b should be accepted before revocation")
	}
	after := instanceValidator(t, "tok-a")
	if after.Authorize("tok-b", "api.example.com") {
		t.Fatal("removed token must be rejected")
	}
	if !after.Authorize("tok-a", "api.example.com") {
		t.Fatal("remaining token must still work")
	}
}

func TestInstanceNoValidTokens(t *testing.T) {
	for _, raw := range []string{"", " ", ",", " , ,, "} {
		_, err := auth.New(config.Config{
			AuthEnabled:   true,
			AuthMode:      config.AuthModeInstance,
			InstanceToken: raw,
		})
		if err == nil {
			t.Fatalf("InstanceToken %q: expected startup error", raw)
		}
	}
}
