package proxy

import (
	"net"
	"net/http/httputil"
	"net/netip"
	"strings"
)

// setForwarded adds X-Forwarded-For / -Host / -Proto to the upstream request
// (UPSTREAM_FORWARD_CLIENT_IP). ReverseProxy removes every inbound X-Forwarded-*
// before Rewrite, so a trusted inbound chain is copied back from pr.In explicitly.
// Trusted means: no trusted-proxy list is configured (ingress-controller default),
// or the connecting address is in it. An untrusted peer's values are dropped and only
// the connecting address is used.
func setForwarded(pr *httputil.ProxyRequest, trusted []netip.Prefix) {
	inbound := inboundTrusted(pr.In.RemoteAddr, trusted)
	if inbound {
		if prior := pr.In.Header.Values("X-Forwarded-For"); len(prior) > 0 {
			pr.Out.Header["X-Forwarded-For"] = append([]string(nil), prior...)
		}
	}
	// Appends the connecting address to the chain above; sets Host and Proto from the
	// proxy's own view of the connection.
	pr.SetXForwarded()
	if !inbound {
		return
	}
	if v := strings.TrimSpace(pr.In.Header.Get("X-Forwarded-Host")); v != "" {
		pr.Out.Header.Set("X-Forwarded-Host", v)
	}
	switch v := strings.ToLower(strings.TrimSpace(pr.In.Header.Get("X-Forwarded-Proto"))); v {
	case "http", "https":
		pr.Out.Header.Set("X-Forwarded-Proto", v)
	}
}

// inboundTrusted reports whether inbound X-Forwarded-* from remoteAddr are honoured.
func inboundTrusted(remoteAddr string, trusted []netip.Prefix) bool {
	if len(trusted) == 0 {
		return true
	}
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		return false
	}
	addr, err := netip.ParseAddr(host)
	if err != nil {
		return false
	}
	addr = addr.Unmap()
	for _, p := range trusted {
		if p.Contains(addr) {
			return true
		}
	}
	return false
}
