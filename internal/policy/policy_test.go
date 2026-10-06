package policy_test

import (
	"testing"

	"github.com/denisitpro/lux-l7/internal/policy"
)

func TestPolicyDenyWins(t *testing.T) {
	p := policy.New([]string{"api.example.com"}, nil, []string{"api.example.com"})
	if p.Allowed("api.example.com") {
		t.Fatal("expected deny to win")
	}
}

func TestPolicySuffix(t *testing.T) {
	p := policy.New(nil, []string{".example.com"}, nil)
	if !p.Allowed("api.example.com") {
		t.Fatal("suffix should allow")
	}
	if p.Allowed("evil.net") {
		t.Fatal("suffix should block")
	}
}

func TestPolicyTrailingDotAndCase(t *testing.T) {
	deny := policy.New(nil, nil, []string{"Localhost"})
	for _, a := range []string{"localhost", "localhost.", "LOCALHOST:8080", "localhost.:8080"} {
		if deny.Allowed(a) {
			t.Fatalf("deny must match %q", a)
		}
	}
	allow := policy.New([]string{"api.example.com."}, []string{"example.org."}, nil)
	for _, a := range []string{"api.example.com", "api.example.com.", "api.example.com.:443", "www.example.org."} {
		if !allow.Allowed(a) {
			t.Fatalf("allow must match %q", a)
		}
	}
	if allow.Allowed("other.example.net.") {
		t.Fatal("unlisted host must stay blocked")
	}
}
