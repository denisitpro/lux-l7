// Package policy decides whether a target host may be proxied.
package policy

import (
	"net"
	"strings"
)

// Policy enforces allow/deny lists from configuration.
type Policy struct {
	allowHosts    map[string]struct{}
	allowSuffixes []string
	denyHosts     map[string]struct{}
}

// New builds a policy from host and suffix lists (already lowercased).
func New(allowHosts, allowSuffixes, denyHosts []string) *Policy {
	p := &Policy{
		allowHosts:    make(map[string]struct{}, len(allowHosts)),
		allowSuffixes: make([]string, 0, len(allowSuffixes)),
		denyHosts:     make(map[string]struct{}, len(denyHosts)),
	}
	for _, h := range allowHosts {
		if h = normalizeHost(h); h != "" {
			p.allowHosts[h] = struct{}{}
		}
	}
	for _, s := range allowSuffixes {
		s = strings.TrimSpace(strings.ToLower(s))
		if s == "" {
			continue
		}
		if !strings.HasPrefix(s, ".") {
			s = "." + s
		}
		p.allowSuffixes = append(p.allowSuffixes, s)
	}
	for _, h := range denyHosts {
		if h = normalizeHost(h); h != "" {
			p.denyHosts[h] = struct{}{}
		}
	}
	return p
}

// Allowed reports whether authority (host or host:port) may be used as upstream.
func (p *Policy) Allowed(authority string) bool {
	if p == nil {
		return true
	}
	host := hostname(authority)
	if host == "" {
		return false
	}
	if _, denied := p.denyHosts[host]; denied {
		return false
	}
	if len(p.allowHosts) == 0 && len(p.allowSuffixes) == 0 {
		return true
	}
	if _, ok := p.allowHosts[host]; ok {
		return true
	}
	for _, suf := range p.allowSuffixes {
		if host == strings.TrimPrefix(suf, ".") || strings.HasSuffix(host, suf) {
			return true
		}
	}
	return false
}

func hostname(authority string) string {
	authority = normalizeHost(authority)
	if authority == "" {
		return ""
	}
	host, _, err := net.SplitHostPort(authority)
	if err != nil {
		return authority
	}
	return host
}

func normalizeHost(raw string) string {
	return strings.ToLower(strings.TrimSpace(raw))
}
