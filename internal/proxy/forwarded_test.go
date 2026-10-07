package proxy_test

import (
	"net/http"
	"net/netip"
	"testing"

	"github.com/denisitpro/lux-l7/internal/config"
	"github.com/denisitpro/lux-l7/internal/headers"
)

// forwardedSeen sends one request with the given client headers through a header-mode
// proxy and returns the X-Forwarded-* the upstream saw.
func forwardedSeen(t *testing.T, cfg config.Config, in map[string]string) http.Header {
	t.Helper()
	var seen http.Header
	up := okUpstream(t, func(r *http.Request) { seen = r.Header.Clone() })
	url := newProxy(t, cfg)
	hdr := map[string]string{headers.TargetHost: up.Listener.Addr().String(), headers.TargetScheme: "http"}
	for k, v := range in {
		hdr[k] = v
	}
	if code, _ := do(t, url, hdr); code != http.StatusOK {
		t.Fatalf("status=%d", code)
	}
	return seen
}

var clientHdrs = map[string]string{
	"X-Forwarded-For":   "203.0.113.9",
	"X-Forwarded-Host":  "front.example.com",
	"X-Forwarded-Proto": "https",
}

func TestForwardOffSendsNoForwardedHeaders(t *testing.T) {
	seen := forwardedSeen(t, httpCfg(), clientHdrs)
	for _, h := range []string{"X-Forwarded-For", "X-Forwarded-Host", "X-Forwarded-Proto", "Forwarded"} {
		if v := seen.Get(h); v != "" {
			t.Fatalf("%s reached upstream: %q", h, v)
		}
	}
}

func TestForwardOnDirectClient(t *testing.T) {
	cfg := httpCfg()
	cfg.UpstreamForwardClientIP = true
	seen := forwardedSeen(t, cfg, nil)
	if got := seen.Get("X-Forwarded-For"); got != "127.0.0.1" {
		t.Fatalf("XFF = %q", got)
	}
	if seen.Get("X-Forwarded-Host") == "" || seen.Get("X-Forwarded-Proto") != "http" {
		t.Fatalf("host/proto = %q / %q", seen.Get("X-Forwarded-Host"), seen.Get("X-Forwarded-Proto"))
	}
}

func TestForwardOnInboundTrustedByDefault(t *testing.T) {
	cfg := httpCfg()
	cfg.UpstreamForwardClientIP = true
	seen := forwardedSeen(t, cfg, clientHdrs)
	if got := seen.Get("X-Forwarded-For"); got != "203.0.113.9, 127.0.0.1" {
		t.Fatalf("XFF = %q", got)
	}
	if got := seen.Get("X-Forwarded-Host"); got != "front.example.com" {
		t.Fatalf("XFH = %q", got)
	}
	if got := seen.Get("X-Forwarded-Proto"); got != "https" {
		t.Fatalf("XFP = %q", got)
	}
}

func TestForwardTrustedListInsideAndOutside(t *testing.T) {
	inside := httpCfg()
	inside.UpstreamForwardClientIP = true
	inside.UpstreamTrustedProxies = []netip.Prefix{netip.MustParsePrefix("127.0.0.0/8")}
	seen := forwardedSeen(t, inside, clientHdrs)
	if got := seen.Get("X-Forwarded-For"); got != "203.0.113.9, 127.0.0.1" {
		t.Fatalf("inside: XFF = %q", got)
	}

	outside := httpCfg()
	outside.UpstreamForwardClientIP = true
	outside.UpstreamTrustedProxies = []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8"), netip.MustParsePrefix("2001:db8::/32")}
	seen = forwardedSeen(t, outside, clientHdrs)
	if got := seen.Get("X-Forwarded-For"); got != "127.0.0.1" {
		t.Fatalf("outside: XFF = %q", got)
	}
	if got := seen.Get("X-Forwarded-Host"); got == "front.example.com" || got == "" {
		t.Fatalf("outside: XFH = %q", got)
	}
	if got := seen.Get("X-Forwarded-Proto"); got != "http" {
		t.Fatalf("outside: XFP = %q", got)
	}
}
