package policy_test

import (
	"errors"
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
	if p.Allowed("denied.invalid") {
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

func TestSplitAuthority(t *testing.T) {
	ok := []struct{ in, host, port string }{
		{"Example.COM", "example.com", ""},
		{"example.com.", "example.com", ""},
		{"Example.com.:443", "example.com", "443"},
		{"[::1]:8443", "::1", "8443"},
		{"127.0.0.1:65535", "127.0.0.1", "65535"},
	}
	for _, c := range ok {
		host, port, err := policy.SplitAuthority(c.in)
		if err != nil || host != c.host || port != c.port {
			t.Errorf("%q: got %q %q %v, want %q %q", c.in, host, port, err, c.host, c.port)
		}
	}
	for _, in := range []string{"", " ", ".", ":443", "u@example.com", "example.com/x", "example.com?x", "https://example.com"} {
		if _, _, err := policy.SplitAuthority(in); !errors.Is(err, policy.ErrInvalidAuthority) && !errors.Is(err, policy.ErrInvalidPort) {
			t.Errorf("%q: want error, got %v", in, err)
		}
	}
	for _, in := range []string{"example.com:0", "example.com:65536", "example.com:x", "example.com:", "[::1]"} {
		if _, _, err := policy.SplitAuthority(in); !errors.Is(err, policy.ErrInvalidPort) {
			t.Errorf("%q: want ErrInvalidPort, got %v", in, err)
		}
	}
}
