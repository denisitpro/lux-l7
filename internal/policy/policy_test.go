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
