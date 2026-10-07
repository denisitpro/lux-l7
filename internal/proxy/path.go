package proxy

import (
	"errors"
	"net"
	"net/http"
	"net/url"
	"strings"
)

// pathPrefix introduces the target authority in path mode: /h/<host>[:port]/<path>.
const pathPrefix = "/h/"

// parsePathTarget reads the target from the request path (path mode) and returns it
// with the escaped path to forward ("/" when the client sent none). The scheme is
// always the configured default: path mode has no per-request scheme override.
func parsePathTarget(r *http.Request, defaultScheme string, allowHTTP bool) (target, string, error) {
	rest, ok := strings.CutPrefix(r.URL.EscapedPath(), pathPrefix)
	if !ok {
		return target{}, "", errors.New("path must start with " + pathPrefix + "<host>")
	}
	rawHost, tail, _ := strings.Cut(rest, "/")
	if rawHost == "" {
		return target{}, "", errors.New("missing target host in path")
	}
	// A hostname never carries percent-encoding; refusing it keeps %2F or a zone
	// from smuggling a different authority past the policy.
	if strings.Contains(rawHost, "%") {
		return target{}, "", errors.New("invalid target host in path")
	}
	if defaultScheme == "http" && !allowHTTP {
		return target{}, "", errors.New("http upstream scheme is disabled")
	}
	t, err := newTarget(defaultScheme, rawHost, "target host in path")
	if err != nil {
		return target{}, "", err
	}
	return t, "/" + tail, nil
}

// setEscapedPath makes u.Path/u.RawPath carry escaped verbatim, so encoded slashes
// and other escapes reach the upstream exactly as the client wrote them.
func setEscapedPath(u *url.URL, escaped string) {
	p, err := url.PathUnescape(escaped)
	if err != nil {
		// The server already parsed this path; keep the encoded form as-is.
		p = escaped
	}
	u.Path, u.RawPath = p, escaped
}

// modifyResponse strips an upstream-sent origin marker and, in path mode, points
// redirects at the target back through the proxy. Header-mode clients name the
// target themselves and keep the URL they asked for.
func (h *Handler) modifyResponse(resp *http.Response) error {
	_ = StripOriginMarker(resp)
	if !h.path {
		return nil
	}
	t, ok := resp.Request.Context().Value(targetKey).(target)
	if !ok {
		return nil
	}
	for _, name := range []string{"Location", "Content-Location"} {
		vals := resp.Header.Values(name)
		for i, v := range vals {
			vals[i] = rewriteLocation(v, t)
		}
	}
	return nil
}

// rewriteLocation maps a Location value to the /h/<host> form when it points at the
// target: an absolute URL of the same scheme, host and effective port, a
// scheme-relative one, or an absolute path. Anything else is returned unchanged.
func rewriteLocation(loc string, t target) string {
	front := pathPrefix + t.host
	if strings.HasPrefix(loc, "/") && !strings.HasPrefix(loc, "//") {
		return front + loc
	}
	u, err := url.Parse(loc)
	if err != nil || u.Opaque != "" || u.User != nil || u.Host == "" {
		return loc
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme == "" {
		scheme = t.scheme // "//host/path" keeps the scheme of the response
	}
	if scheme != t.scheme || !sameAuthority(u.Host, t.host, scheme) {
		return loc
	}
	out := front + "/"
	if p := u.EscapedPath(); p != "" {
		out = front + p
	}
	if u.RawQuery != "" || u.ForceQuery {
		out += "?" + u.RawQuery
	}
	if u.Fragment != "" {
		out += "#" + u.EscapedFragment()
	}
	return out
}

// sameAuthority compares two authorities by lowercase host and effective port.
func sameAuthority(a, b, scheme string) bool {
	ha, pa := splitDefaultPort(a, scheme)
	hb, pb := splitDefaultPort(b, scheme)
	return ha == hb && pa == pb
}

func splitDefaultPort(authority, scheme string) (host, port string) {
	host, port, err := net.SplitHostPort(authority)
	if err != nil {
		host, port = strings.Trim(authority, "[]"), ""
	}
	if port == "" {
		port = "443"
		if scheme == "http" {
			port = "80"
		}
	}
	return strings.TrimSuffix(strings.ToLower(host), "."), port
}
