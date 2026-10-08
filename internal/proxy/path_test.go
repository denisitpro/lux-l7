package proxy_test

import (
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/denisitpro/lux-l7/internal/config"
	"github.com/denisitpro/lux-l7/internal/headers"
)

func pathCfg() config.Config {
	cfg := httpCfg()
	cfg.ProxyMode = config.ProxyModePath
	return cfg
}

// get sends GET <proxyURL><uri> with hdr and returns the response without following redirects.
func get(t *testing.T, proxyURL, uri string, hdr map[string]string) (*http.Response, string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, proxyURL+uri, nil)
	if err != nil {
		t.Fatal(err)
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	res, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	body, _ := io.ReadAll(res.Body)
	return res, string(body)
}

func TestPathModeForwardsPathAndQuery(t *testing.T) {
	var got *http.Request
	up := okUpstream(t, func(r *http.Request) { got = r })
	authority := up.Listener.Addr().String()
	url := newProxy(t, pathCfg())

	res, body := get(t, url, "/h/"+authority+"/v1/ping?a=1&b=x%20y", nil)
	if res.StatusCode != http.StatusOK || body != "pong" {
		t.Fatalf("status=%d body=%s", res.StatusCode, body)
	}
	if got.RequestURI != "/v1/ping?a=1&b=x%20y" {
		t.Fatalf("upstream request URI = %q", got.RequestURI)
	}
	if got.Host != authority {
		t.Fatalf("upstream Host = %q, want %q", got.Host, authority)
	}
}

func TestPathModeEmptyPath(t *testing.T) {
	var uris []string
	up := okUpstream(t, func(r *http.Request) { uris = append(uris, r.RequestURI) })
	authority := up.Listener.Addr().String()
	url := newProxy(t, pathCfg())

	for _, in := range []string{"/h/" + authority, "/h/" + authority + "/", "/h/" + authority + "?q=1"} {
		if res, _ := get(t, url, in, nil); res.StatusCode != http.StatusOK {
			t.Fatalf("%s: status=%d", in, res.StatusCode)
		}
	}
	want := []string{"/", "/", "/?q=1"}
	for i := range want {
		if uris[i] != want[i] {
			t.Fatalf("upstream URIs = %v, want %v", uris, want)
		}
	}
}

func TestPathModeKeepsEscapesVerbatim(t *testing.T) {
	var got string
	up := okUpstream(t, func(r *http.Request) { got = r.RequestURI })
	authority := up.Listener.Addr().String()
	url := newProxy(t, pathCfg())

	for _, rest := range []string{"/a%2Fb/c%2fd", "//double//slash", "/x/../y", "/p%3Fq%23r"} {
		if res, _ := get(t, url, "/h/"+authority+rest, nil); res.StatusCode != http.StatusOK {
			t.Fatalf("%s: status=%d", rest, res.StatusCode)
		}
		if got != rest {
			t.Fatalf("upstream got %q, want %q", got, rest)
		}
	}
}

func TestPathModeIgnoresAndStripsRoutingHeaders(t *testing.T) {
	var got http.Header
	up := okUpstream(t, func(r *http.Request) { got = r.Header.Clone() })
	authority := up.Listener.Addr().String()
	url := newProxy(t, pathCfg())

	// The headers name a different host and an https scheme; neither may matter.
	res, _ := get(t, url, "/h/"+authority+"/x", map[string]string{
		headers.TargetHost:   "other.test",
		headers.TargetScheme: "https",
		headers.ProxyToken:   "secret",
	})
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status=%d", res.StatusCode)
	}
	for _, h := range []string{headers.TargetHost, headers.TargetScheme, headers.ProxyToken} {
		if got.Get(h) != "" {
			t.Fatalf("%s leaked to upstream", h)
		}
	}
}

func TestPathModeBadTargets(t *testing.T) {
	url := newProxy(t, pathCfg())
	for _, uri := range []string{
		"/",
		"/v1/ping",
		"/h",
		"/h/",
		"/h//x",
		"/hx/a.test/x",
		"/h/a.test:abc/x",
		"/h/a.test:0/x",
		"/h/a.test:65536/x",
		"/h/user@a.test/x",
		"/h/a.test:80@127.0.0.1/x",
		"/h/a%2Ftest/x",
		"/h/a.test%3A80/x",
		"/h/[::1/x",
	} {
		res, body := get(t, url, uri, map[string]string{headers.TargetHost: "127.0.0.1"})
		if res.StatusCode != http.StatusBadRequest || !strings.Contains(body, `"bad_target"`) {
			t.Fatalf("%q: status=%d body=%s", uri, res.StatusCode, body)
		}
	}
}

func TestPathModeHTTPSchemeGate(t *testing.T) {
	cfg := pathCfg()
	cfg.UpstreamAllowHTTP = false
	res, body := get(t, newProxy(t, cfg), "/h/a.test/x", nil)
	if res.StatusCode != http.StatusBadRequest || !strings.Contains(body, "bad_target") {
		t.Fatalf("status=%d body=%s", res.StatusCode, body)
	}
}

func TestPathModeBracketedIPv6(t *testing.T) {
	cfg := pathCfg()
	cfg.AllowHosts = []string{"example.com"}
	url := newProxy(t, cfg)
	// Parsed as a host, so policy (not the parser) refuses it.
	res, body := get(t, url, "/h/[2001:db8::1]:443/x", nil)
	if res.StatusCode != http.StatusForbidden || !strings.Contains(body, "upstream host not allowed") {
		t.Fatalf("status=%d body=%s", res.StatusCode, body)
	}

	ln, err := net.Listen("tcp6", "[::1]:0")
	if err != nil {
		t.Skip("no IPv6 loopback")
	}
	var got string
	up := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.RequestURI
		_, _ = w.Write([]byte("pong"))
	}))
	up.Listener = ln
	up.Start()
	t.Cleanup(up.Close)

	cfg.AllowHosts = []string{"::1"}
	res, body = get(t, newProxy(t, cfg), "/h/"+ln.Addr().String()+"/v6?x=1", nil)
	if res.StatusCode != http.StatusOK || body != "pong" || got != "/v6?x=1" {
		t.Fatalf("status=%d body=%s uri=%q", res.StatusCode, body, got)
	}
}

func TestPathModeAuthPolicyAndPorts(t *testing.T) {
	up := okUpstream(t, nil)
	authority := up.Listener.Addr().String()
	port := authority[strings.LastIndex(authority, ":")+1:]

	cfg := pathCfg()
	cfg.AuthEnabled = true
	cfg.AuthMode = config.AuthModeInstance
	cfg.InstanceToken = "tok"
	cfg.UpstreamAllowPorts = []int{443}
	cfg.DenyHosts = []string{"denied.test"}
	url := newProxy(t, cfg)

	tok := map[string]string{headers.ProxyToken: "tok"}
	for _, c := range []struct {
		uri  string
		hdr  map[string]string
		code int
		body string
	}{
		{"/h/" + authority + "/", nil, http.StatusUnauthorized, "unauthorized"},
		// A token in the path or query is not read.
		{"/h/" + authority + "/?token=tok", nil, http.StatusUnauthorized, "unauthorized"},
		{"/h/denied.test:443/", tok, http.StatusForbidden, "upstream host not allowed"},
		{"/h/" + authority + "/", tok, http.StatusForbidden, "upstream port not allowed"},
	} {
		res, body := get(t, url, c.uri, c.hdr)
		if res.StatusCode != c.code || !strings.Contains(body, c.body) {
			t.Fatalf("%s: status=%d body=%s", c.uri, res.StatusCode, body)
		}
	}

	cfg.UpstreamAllowPorts = nil
	res, body := get(t, newProxy(t, cfg), "/h/"+authority+"/", tok)
	if res.StatusCode != http.StatusOK || body != "pong" {
		t.Fatalf("authorized: status=%d body=%s port=%s", res.StatusCode, body, port)
	}
}

func TestPathModeBodyLimit(t *testing.T) {
	up := okUpstream(t, nil)
	cfg := pathCfg()
	cfg.UpstreamMaxBytes = 4
	req, err := http.NewRequest(http.MethodPost, newProxy(t, cfg)+"/h/"+up.Listener.Addr().String()+"/", strings.NewReader("too large body"))
	if err != nil {
		t.Fatal(err)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("status=%d", res.StatusCode)
	}
}

func TestPathModeRewritesLocationHeaders(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if v := r.Header.Get("X-Loc"); v != "" {
			w.Header().Set("Location", v)
			w.Header().Set("Content-Location", v)
		}
		w.Header().Add("Set-Cookie", "sid=1; Path=/")
		w.WriteHeader(http.StatusFound)
	}))
	t.Cleanup(up.Close)
	authority := up.Listener.Addr().String()
	front := "/h/" + authority
	url := newProxy(t, pathCfg())

	for _, c := range []struct{ in, want string }{
		{"http://" + authority + "/a/b?x=1#f", front + "/a/b?x=1#f"},
		{"HTTP://" + authority, front + "/"},
		{"//" + authority + "/s", front + "/s"},
		{"/abs/path?q=1", front + "/abs/path?q=1"},
		{"http://other.test/a", "http://other.test/a"},
		{"https://" + authority + "/a", "https://" + authority + "/a"},
		{"http://127.0.0.1:1/a", "http://127.0.0.1:1/a"},
		{"relative/path", "relative/path"},
		{"?q=1", "?q=1"},
		{"mailto:a@b.test", "mailto:a@b.test"},
	} {
		res, _ := get(t, url, "/h/"+authority+"/r", map[string]string{"X-Loc": c.in})
		if got := res.Header.Get("Location"); got != c.want {
			t.Fatalf("Location %q -> %q, want %q", c.in, got, c.want)
		}
		if got := res.Header.Get("Content-Location"); got != c.want {
			t.Fatalf("Content-Location %q -> %q, want %q", c.in, got, c.want)
		}
		if got := res.Header.Get("Set-Cookie"); got != "sid=1; Path=/" {
			t.Fatalf("Set-Cookie rewritten: %q", got)
		}
	}
}

func TestHeaderModeLeavesLocationAlone(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", "/abs")
		w.WriteHeader(http.StatusFound)
	}))
	t.Cleanup(up.Close)
	res, _ := get(t, newProxy(t, httpCfg()), "/", map[string]string{headers.TargetHost: up.Listener.Addr().String()})
	if got := res.Header.Get("Location"); got != "/abs" {
		t.Fatalf("Location = %q", got)
	}
}

func TestPathModeBracketedIPv6WithoutPortReachesSSRFGuard(t *testing.T) {
	cfg := pathCfg()
	cfg.UpstreamAllowPrivate = false
	res, body := get(t, newProxy(t, cfg), "/h/[::1]/x", nil)
	if res.StatusCode != http.StatusForbidden || !strings.Contains(body, "upstream address not allowed") {
		t.Fatalf("status=%d body=%s", res.StatusCode, body)
	}
}
