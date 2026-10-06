// Package route parses static routes and matches incoming host + path.
package route

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/url"
	"strings"
)

// Route is a validated static route entry.
type Route struct {
	Host          string
	PathPrefix    string
	Upstream      url.URL
	HostHeader    string
	TLSServerName string
	RewritePrefix string

	// OutHost is the fixed outgoing Host: host_header, else the upstream authority.
	OutHost string
	// ServerName is the TLS SNI for https upstreams (empty for http).
	ServerName string
}

type routeJSON struct {
	Host          string `json:"host"`
	PathPrefix    string `json:"path_prefix"`
	Upstream      string `json:"upstream"`
	HostHeader    string `json:"host_header"`
	TLSServerName string `json:"tls_server_name"`
	RewritePrefix string `json:"rewrite_prefix"`
}

// ParseJSON decodes a ROUTES env value into validated routes (array order preserved).
func ParseJSON(raw string) ([]Route, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, fmt.Errorf("ROUTES: empty")
	}
	dec := json.NewDecoder(strings.NewReader(raw))
	dec.DisallowUnknownFields()
	var items []routeJSON
	if err := dec.Decode(&items); err != nil {
		return nil, fmt.Errorf("ROUTES: %w", err)
	}
	if len(items) == 0 {
		return nil, fmt.Errorf("ROUTES: must contain at least one route")
	}
	out := make([]Route, 0, len(items))
	for i, item := range items {
		rt, err := validateRoute(i, item)
		if err != nil {
			return nil, err
		}
		out = append(out, rt)
	}
	return out, nil
}

func validateRoute(i int, item routeJSON) (Route, error) {
	prefix := fmt.Sprintf("ROUTES[%d]", i)

	host := normalizeRequestHost(item.Host)
	if host == "" {
		return Route{}, fmt.Errorf("%s: host is required", prefix)
	}
	if strings.HasPrefix(host, ".") {
		if len(host) < 2 {
			return Route{}, fmt.Errorf("%s: invalid host suffix %q", prefix, item.Host)
		}
	} else if strings.ContainsAny(host, "/\\") {
		return Route{}, fmt.Errorf("%s: invalid host %q", prefix, item.Host)
	}

	pathPrefix := strings.TrimSpace(item.PathPrefix)
	if pathPrefix == "" {
		return Route{}, fmt.Errorf("%s: path_prefix is required", prefix)
	}
	if !strings.HasPrefix(pathPrefix, "/") {
		return Route{}, fmt.Errorf("%s: path_prefix must start with /", prefix)
	}
	// A trailing slash is not part of the prefix: "/api/" means "/api" (boundary match).
	if pathPrefix = strings.TrimRight(pathPrefix, "/"); pathPrefix == "" {
		pathPrefix = "/"
	}

	upstreamRaw := strings.TrimSpace(item.Upstream)
	if upstreamRaw == "" {
		return Route{}, fmt.Errorf("%s: upstream is required", prefix)
	}
	u, err := parseUpstreamURL(upstreamRaw)
	if err != nil {
		return Route{}, fmt.Errorf("%s: upstream: %w", prefix, err)
	}

	hostHeader := strings.TrimSpace(item.HostHeader)
	tlsName := strings.TrimSpace(item.TLSServerName)
	if tlsName != "" && u.Scheme != "https" {
		return Route{}, fmt.Errorf("%s: tls_server_name is only valid for https upstream", prefix)
	}

	rewrite := item.RewritePrefix
	if rewrite != "" && !strings.HasPrefix(rewrite, "/") {
		return Route{}, fmt.Errorf("%s: rewrite_prefix must start with / when set", prefix)
	}

	// One route has one connection pool, so the outgoing Host (and therefore the
	// SNI) must be fixed per route: a Host varying per request would break
	// SNI == Host, which Cloudflare-fronted upstreams enforce. The default is the
	// upstream authority as written (host plus port); host_header overrides it and,
	// like the default, keeps its port (it is sent as-is); SNI never carries one.
	outHost := strings.ToLower(u.Host)
	if hostHeader != "" {
		outHost = strings.ToLower(hostHeader)
	}
	serverName := ""
	if u.Scheme == "https" {
		hostName := normalizeRequestHost(outHost)
		serverName = hostName
		if tlsName != "" {
			serverName = normalizeRequestHost(tlsName)
			if serverName != hostName {
				slog.Default().Warn("SNI differs from Host; Cloudflare-fronted upstreams reject this",
					"route", i, "sni", serverName, "host", hostName)
			}
		}
	}

	return Route{
		OutHost:       outHost,
		ServerName:    serverName,
		Host:          host,
		PathPrefix:    pathPrefix,
		Upstream:      *u,
		HostHeader:    hostHeader,
		TLSServerName: tlsName,
		RewritePrefix: rewrite,
	}, nil
}

func parseUpstreamURL(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return nil, err
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" {
		return nil, fmt.Errorf("scheme must be http or https")
	}
	if u.User != nil {
		return nil, fmt.Errorf("userinfo not allowed")
	}
	if u.Host == "" {
		return nil, fmt.Errorf("host is required")
	}
	if u.RawQuery != "" || u.Fragment != "" {
		return nil, fmt.Errorf("query and fragment not allowed")
	}
	path := u.EscapedPath()
	if path != "" && path != "/" {
		return nil, fmt.Errorf("path not allowed")
	}
	u.Path = ""
	u.RawPath = ""
	return u, nil
}

// MatchHost reports whether requestHost matches routeHost (exact or suffix).
func MatchHost(requestHost, routeHost string) bool {
	requestHost = normalizeRequestHost(requestHost)
	routeHost = normalizeRequestHost(routeHost)
	if requestHost == "" || routeHost == "" {
		return false
	}
	if !strings.HasPrefix(routeHost, ".") {
		return requestHost == routeHost
	}
	base := strings.TrimPrefix(routeHost, ".")
	return requestHost == base || strings.HasSuffix(requestHost, routeHost)
}

// PathMatches reports whether path matches pathPrefix on a segment boundary.
func PathMatches(pathPrefix, path string) bool {
	if pathPrefix == "/" {
		return true
	}
	if !strings.HasPrefix(path, pathPrefix) {
		return false
	}
	if len(path) == len(pathPrefix) {
		return true
	}
	return path[len(pathPrefix)] == '/'
}

// Match returns the index of the first matching route, or -1.
func Match(routes []Route, requestHost, path string) int {
	host := normalizeRequestHost(requestHost)
	for i, rt := range routes {
		if !MatchHost(host, rt.Host) {
			continue
		}
		if PathMatches(rt.PathPrefix, path) {
			return i
		}
	}
	return -1
}

// RewritePath applies rewrite_prefix after a path_prefix match; query is handled separately.
func RewritePath(path, pathPrefix, rewritePrefix string) string {
	if rewritePrefix == "" {
		return path
	}
	if pathPrefix == "/" {
		rest := strings.TrimPrefix(path, "/")
		if rest == "" {
			return rewritePrefix
		}
		return joinPathPrefix(rewritePrefix, "/"+rest)
	}
	rest := strings.TrimPrefix(path, pathPrefix)
	return joinPathPrefix(rewritePrefix, rest)
}

func joinPathPrefix(base, rest string) string {
	if rest == "" || rest == "/" {
		if base == "" {
			return "/"
		}
		out := strings.TrimSuffix(base, "/")
		if out == "" {
			return "/"
		}
		return out
	}
	if !strings.HasPrefix(rest, "/") {
		rest = "/" + rest
	}
	base = strings.TrimSuffix(base, "/")
	if base == "" {
		return rest
	}
	return base + rest
}

// NormalizeRequestHost lowercases host, strips port and trailing dot.
func NormalizeRequestHost(raw string) string {
	return normalizeRequestHost(raw)
}

func normalizeRequestHost(raw string) string {
	raw = strings.ToLower(strings.TrimSpace(raw))
	raw = strings.TrimSuffix(raw, ".")
	if raw == "" {
		return ""
	}
	host, _, err := net.SplitHostPort(raw)
	if err != nil {
		return raw
	}
	return host
}
