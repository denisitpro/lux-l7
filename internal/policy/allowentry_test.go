package policy_test

import (
	"testing"

	"github.com/denisitpro/lux-l7/internal/policy"
)

func TestPolicyAllowEntry(t *testing.T) {
	p := policy.New([]string{"api.example.com"}, []string{"example.org"}, []string{"bad.example.org"})
	for _, tc := range []struct{ in, want string }{
		{"api.example.com", "api.example.com"},
		{"API.example.com.:443", "api.example.com"},
		{"www.example.org", ".example.org"},
		{"example.org", ".example.org"},
		{"denied.invalid", ""},
		{"", ""},
	} {
		if got := p.AllowEntry(tc.in); got != tc.want {
			t.Fatalf("AllowEntry(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
	if got := policy.New(nil, nil, nil).AllowEntry("api.example.com"); got != "" {
		t.Fatalf("open policy: %q, want empty", got)
	}
	var nilPolicy *policy.Policy
	if nilPolicy.AllowEntry("api.example.com") != "" {
		t.Fatal("nil policy must return empty")
	}
}
