package proxy

import (
	"net/http"
	"net/http/httputil"
	"net/netip"
	"testing"
)

func TestSetForwardedIPv6(t *testing.T) {
	in, _ := http.NewRequest(http.MethodGet, "http://front.example.com/", nil)
	in.RemoteAddr = "[2001:db8::7]:5555"
	in.Header.Set("X-Forwarded-For", "203.0.113.9")
	out := in.Clone(in.Context())
	out.Header.Del("X-Forwarded-For")
	pr := &httputil.ProxyRequest{In: in, Out: out}

	setForwarded(pr, []netip.Prefix{netip.MustParsePrefix("2001:db8::/32")})
	if got := out.Header.Get("X-Forwarded-For"); got != "203.0.113.9, 2001:db8::7" {
		t.Fatalf("trusted v6: XFF = %q", got)
	}

	out2 := in.Clone(in.Context())
	out2.Header.Del("X-Forwarded-For")
	pr2 := &httputil.ProxyRequest{In: in, Out: out2}
	setForwarded(pr2, []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8")})
	if got := out2.Header.Get("X-Forwarded-For"); got != "2001:db8::7" {
		t.Fatalf("untrusted v6: XFF = %q", got)
	}
}

func TestInboundTrustedMappedIPv4(t *testing.T) {
	if !inboundTrusted("[::ffff:10.1.2.3]:80", []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8")}) {
		t.Fatal("mapped IPv4 peer must match an IPv4 prefix")
	}
	if inboundTrusted("garbage", []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8")}) {
		t.Fatal("unparsable peer must be untrusted")
	}
}
