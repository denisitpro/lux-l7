package metrics

import (
	"net"
	"strings"
)

// Fixed label values. Every host label is one of: a configured name or a sentinel below.
const (
	// Other is the host label for anything the proxy was not told about.
	Other = "other"
	// Any is the ingress label of a route that names no host.
	Any = "any"
)

// Hosts maps raw hosts and configured names to host label values. It is the only place
// that decides a host label, so the series count is bounded by configuration (the
// ROUTES table, METRICS_INGRESS_HOSTS, the upstream allow list) and never by what a
// client sends.
type Hosts struct {
	ingress map[string]struct{}
}

// NewHosts builds the mapper from the configured front hosts (header mode).
func NewHosts(ingressHosts []string) Hosts {
	h := Hosts{ingress: make(map[string]struct{}, len(ingressHosts))}
	for _, s := range ingressHosts {
		if s = bareHost(s); s != "" {
			h.ingress[s] = struct{}{}
		}
	}
	return h
}

// Ingress labels a client-supplied Host header: the lowercased host without port when
// it is a configured front host, else Other.
func (h Hosts) Ingress(requestHost string) string {
	if host := bareHost(requestHost); host != "" {
		if _, ok := h.ingress[host]; ok {
			return host
		}
	}
	return Other
}

// RouteIngress labels a matched route by its host pattern exactly as configured.
func (Hosts) RouteIngress(pattern string) string {
	if pattern == "" {
		return Any
	}
	return pattern
}

// Upstream labels a header-mode or tunnel target by the allow-list entry that admitted
// it (exact host, or the suffix entry with its leading dot); empty means no entry
// matched or the policy is open, which is Other.
func (Hosts) Upstream(allowEntry string) string {
	if allowEntry == "" {
		return Other
	}
	return allowEntry
}

// RouteUpstream labels a matched route by its configured upstream host.
func (Hosts) RouteUpstream(upstreamHost string) string {
	if upstreamHost == "" {
		return Other
	}
	return upstreamHost
}

// bareHost lowercases raw and drops the port and one trailing dot.
func bareHost(raw string) string {
	raw = strings.ToLower(strings.TrimSpace(raw))
	if host, _, err := net.SplitHostPort(raw); err == nil {
		raw = host
	}
	return strings.TrimSuffix(raw, ".")
}
