// Package policy decides whether a target host may be proxied.
package policy

import (
	"errors"
	"net"
	"net/netip"
	"strconv"
	"strings"
)

// Errors returned by SplitAuthority.
var (
	ErrInvalidAuthority = errors.New("invalid authority")
	ErrInvalidPort      = errors.New("invalid port")
)

// SplitAuthority validates raw as host or host:port and returns the lowercased host
// (one trailing dot dropped, IPv6 brackets removed) and the port ("" when absent).
// A bracketed IPv6 literal may omit the port ("[2001:db8::1]").
// Userinfo, path, query and fragment characters are refused, and a port must be 1..65535.
func SplitAuthority(raw string) (host, port string, err error) {
	raw = strings.TrimSpace(raw)
	if raw == "" || strings.ContainsAny(raw, "/\\?#@") {
		return "", "", ErrInvalidAuthority
	}
	host = strings.ToLower(raw)
	if strings.HasPrefix(host, "[") && strings.HasSuffix(host, "]") {
		// Bracketed IPv6 literal without a port: the scheme default applies.
		host = host[1 : len(host)-1]
		if a, perr := netip.ParseAddr(host); perr != nil || !a.Is6() || a.Zone() != "" {
			return "", "", ErrInvalidAuthority
		}
		return host, "", nil
	}
	if strings.Contains(host, ":") {
		host, port, err = net.SplitHostPort(host)
		if err != nil {
			return "", "", ErrInvalidPort
		}
		if n, err := strconv.ParseUint(port, 10, 16); err != nil || n == 0 {
			return "", "", ErrInvalidPort
		}
	}
	// One trailing dot is the same DNS name; keep it out of policy and dial.
	host = strings.TrimSuffix(host, ".")
	if host == "" {
		return "", "", ErrInvalidAuthority
	}
	return host, port, nil
}

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
		s = normalizeHost(s)
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
	_, ok := p.matchAllow(host)
	return ok
}

// AllowEntry returns the allow-list entry that admits authority: the exact host, or
// the suffix entry as configured (with its leading dot). It is empty when both allow
// lists are empty or nothing matches. Deny lists are not consulted: this names a
// bounded metrics label, it never decides access.
func (p *Policy) AllowEntry(authority string) string {
	if p == nil {
		return ""
	}
	host := hostname(authority)
	if host == "" {
		return ""
	}
	entry, _ := p.matchAllow(host)
	return entry
}

// matchAllow finds the allow entry for a normalized host: exact hosts first, then
// suffixes in configured order.
func (p *Policy) matchAllow(host string) (entry string, ok bool) {
	if _, exact := p.allowHosts[host]; exact {
		return host, true
	}
	for _, suf := range p.allowSuffixes {
		if host == strings.TrimPrefix(suf, ".") || strings.HasSuffix(host, suf) {
			return suf, true
		}
	}
	return "", false
}

func hostname(authority string) string {
	authority = normalizeHost(authority)
	if authority == "" {
		return ""
	}
	host, _, err := net.SplitHostPort(authority)
	if err != nil {
		return strings.Trim(authority, "[]")
	}
	return normalizeHost(host)
}

// normalizeHost lowercases and drops one trailing dot, so "localhost." and
// "localhost" are the same name for deny and allow decisions.
func normalizeHost(raw string) string {
	return strings.TrimSuffix(strings.ToLower(strings.TrimSpace(raw)), ".")
}
