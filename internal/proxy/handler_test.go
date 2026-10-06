package proxy_test

import (
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/denisitpro/lux-l7/internal/auth"
	"github.com/denisitpro/lux-l7/internal/config"
	"github.com/denisitpro/lux-l7/internal/headers"
	"github.com/denisitpro/lux-l7/internal/proxy"
)

func TestForwardToUpstream(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get(headers.TargetHost) != "" {
			t.Fatal("routing header leaked to upstream")
		}
		if r.URL.Path != "/v1/ping" {
			t.Fatalf("path = %q", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("pong"))
	}))
	t.Cleanup(upstream.Close)

	authority := upstream.Listener.Addr().String()
	cfg := config.Config{
		UpstreamScheme:       "http",
		UpstreamAllowHTTP:    true,
		UpstreamAllowPrivate: true,
		AllowHosts:           []string{"127.0.0.1"},
		AuthEnabled:          false,
	}
	v, err := auth.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	h := proxy.New(cfg, v, nil)

	proxySrv := httptest.NewServer(h)
	t.Cleanup(proxySrv.Close)

	req, err := http.NewRequest(http.MethodGet, proxySrv.URL+"/v1/ping", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set(headers.TargetHost, authority)
	req.Header.Set(headers.TargetScheme, "http")

	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	body, _ := io.ReadAll(res.Body)
	if res.StatusCode != http.StatusOK || string(body) != "pong" {
		t.Fatalf("status=%d body=%q", res.StatusCode, body)
	}
}

func TestMissingTargetHost(t *testing.T) {
	cfg := config.Config{AuthEnabled: false}
	v, _ := auth.New(cfg)
	h := proxy.New(cfg, v, nil)
	proxySrv := httptest.NewServer(h)
	t.Cleanup(proxySrv.Close)

	res, err := http.Get(proxySrv.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d", res.StatusCode)
	}
}

// newProxy starts a header-mode proxy with cfg and returns its URL.
func newProxy(t *testing.T, cfg config.Config) string {
	t.Helper()
	v, err := auth.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(proxy.New(cfg, v, nil))
	t.Cleanup(srv.Close)
	return srv.URL
}

// do sends GET / through the proxy with the given headers and returns status and body.
func do(t *testing.T, proxyURL string, hdr map[string]string) (int, string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, proxyURL+"/", nil)
	if err != nil {
		t.Fatal(err)
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	body, _ := io.ReadAll(res.Body)
	return res.StatusCode, string(body)
}

func okUpstream(t *testing.T, onReq func(*http.Request)) *httptest.Server {
	t.Helper()
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if onReq != nil {
			onReq(r)
		}
		_, _ = w.Write([]byte("pong"))
	}))
	t.Cleanup(s.Close)
	return s
}

func httpCfg() config.Config {
	return config.Config{UpstreamScheme: "http", UpstreamAllowHTTP: true, UpstreamAllowPrivate: true, UpstreamMaxBytes: 1 << 20}
}

func TestAuthInstance401And403(t *testing.T) {
	up := okUpstream(t, nil)
	cfg := httpCfg()
	cfg.AllowHosts = []string{"127.0.0.1"}
	cfg.AuthEnabled = true
	cfg.AuthMode = config.AuthModeInstance
	cfg.InstanceToken = "s3cret"
	url := newProxy(t, cfg)
	target := up.Listener.Addr().String()

	if code, _ := do(t, url, map[string]string{headers.TargetHost: target}); code != http.StatusUnauthorized {
		t.Fatalf("no token: status=%d", code)
	}
	if code, _ := do(t, url, map[string]string{headers.TargetHost: target, headers.ProxyToken: "wrong"}); code != http.StatusUnauthorized {
		t.Fatalf("bad token: status=%d", code)
	}
	if code, _ := do(t, url, map[string]string{headers.TargetHost: "evil.example.net", headers.ProxyToken: "s3cret"}); code != http.StatusForbidden {
		t.Fatalf("denied host: status=%d", code)
	}
	if code, body := do(t, url, map[string]string{headers.TargetHost: target, headers.ProxyToken: "s3cret"}); code != http.StatusOK || body != "pong" {
		t.Fatalf("good token: status=%d body=%q", code, body)
	}
}

func TestAuthPerHost(t *testing.T) {
	up := okUpstream(t, nil)
	cfg := httpCfg()
	cfg.AuthEnabled = true
	cfg.AuthMode = config.AuthModePerHost
	cfg.HostTokenJSON = `{"127.0.0.1":"tok-a","localhost":"tok-b"}`
	url := newProxy(t, cfg)
	target := up.Listener.Addr().String() // 127.0.0.1:port

	if code, _ := do(t, url, map[string]string{headers.TargetHost: target, headers.ProxyToken: "tok-a"}); code != http.StatusOK {
		t.Fatalf("own host token: status=%d", code)
	}
	// Token of another host must not unlock this host.
	if code, _ := do(t, url, map[string]string{headers.TargetHost: target, headers.ProxyToken: "tok-b"}); code != http.StatusUnauthorized {
		t.Fatalf("other host token: status=%d", code)
	}
	// Host without a map entry has no valid token at all.
	port := target[strings.LastIndex(target, ":")+1:]
	if code, _ := do(t, url, map[string]string{headers.TargetHost: "unmapped.example.net:" + port, headers.ProxyToken: "tok-a"}); code != http.StatusUnauthorized {
		t.Fatalf("unmapped host: status=%d", code)
	}
}

func TestProxyTokenAndHopsNeverReachUpstream(t *testing.T) {
	var seen http.Header
	up := okUpstream(t, func(r *http.Request) { seen = r.Header.Clone() })
	cfg := httpCfg()
	cfg.AuthEnabled = true
	cfg.AuthMode = config.AuthModeInstance
	cfg.InstanceToken = "s3cret"
	url := newProxy(t, cfg)

	code, _ := do(t, url, map[string]string{
		headers.TargetHost:   up.Listener.Addr().String(),
		headers.TargetScheme: "http",
		headers.ProxyToken:   "s3cret",
		headers.Hops:         "7",
	})
	if code != http.StatusOK {
		t.Fatalf("status=%d", code)
	}
	for _, h := range []string{headers.ProxyToken, headers.TargetHost, headers.TargetScheme, headers.Hops} {
		if v := seen.Get(h); v != "" {
			t.Fatalf("%s leaked to upstream: %q", h, v)
		}
	}
}

func TestBodyLimit413(t *testing.T) {
	up := okUpstream(t, nil)
	cfg := httpCfg()
	cfg.UpstreamMaxBytes = 8
	url := newProxy(t, cfg)

	req, _ := http.NewRequest(http.MethodPost, url+"/", strings.NewReader("0123456789"))
	req.Header.Set(headers.TargetHost, up.Listener.Addr().String())
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("status=%d", res.StatusCode)
	}
}

func TestPolicyCaseTrailingDotAndPort(t *testing.T) {
	var hostSeen string
	up := okUpstream(t, func(r *http.Request) { hostSeen = r.Host })
	port := up.Listener.Addr().String()
	port = port[strings.LastIndex(port, ":")+1:]

	allow := httpCfg()
	allow.AllowHosts = []string{"localhost"}
	allowURL := newProxy(t, allow)
	for _, host := range []string{"localhost:" + port, "LOCALHOST:" + port, "localhost.:" + port} {
		if code, _ := do(t, allowURL, map[string]string{headers.TargetHost: host}); code != http.StatusOK {
			t.Fatalf("allow %q: status=%d", host, code)
		}
	}
	if hostSeen != "localhost:"+port {
		t.Fatalf("upstream Host=%q, want trailing dot dropped", hostSeen)
	}
	if code, _ := do(t, allowURL, map[string]string{headers.TargetHost: "evil.example.net:" + port}); code != http.StatusForbidden {
		t.Fatalf("not in allow list: status=%d", code)
	}

	deny := httpCfg()
	deny.DenyHosts = []string{"localhost"}
	denyURL := newProxy(t, deny)
	for _, host := range []string{"localhost:" + port, "LocalHost:" + port, "localhost.:" + port, "localhost."} {
		if code, _ := do(t, denyURL, map[string]string{headers.TargetHost: host}); code != http.StatusForbidden {
			t.Fatalf("deny %q: status=%d", host, code)
		}
	}
}

func TestBadTargets(t *testing.T) {
	url := newProxy(t, httpCfg())
	for _, host := range []string{
		"a.test:80@127.0.0.1",
		"user@a.test",
		":80",
		".",
		"a.test:",
		"a.test:abc",
		"a.test:0",
		"a.test:65536",
		"a.test:-1",
		"a.test:+80",
	} {
		code, body := do(t, url, map[string]string{headers.TargetHost: host})
		if code != http.StatusBadRequest || !strings.Contains(body, "bad_target") {
			t.Fatalf("%q: status=%d body=%s", host, code, body)
		}
	}
}

func TestSchemeHTTPGate(t *testing.T) {
	up := okUpstream(t, nil)
	target := up.Listener.Addr().String()

	closed := httpCfg()
	closed.UpstreamScheme = "https"
	closed.UpstreamAllowHTTP = false
	code, body := do(t, newProxy(t, closed), map[string]string{headers.TargetHost: target, headers.TargetScheme: "http"})
	if code != http.StatusBadRequest || !strings.Contains(body, "bad_target") {
		t.Fatalf("http scheme refused: status=%d body=%s", code, body)
	}

	open := httpCfg()
	open.UpstreamScheme = "https"
	code, body = do(t, newProxy(t, open), map[string]string{headers.TargetHost: target, headers.TargetScheme: "http"})
	if code != http.StatusOK || body != "pong" {
		t.Fatalf("http scheme allowed: status=%d body=%s", code, body)
	}
}

func TestUpstreamTimeout504(t *testing.T) {
	done := make(chan struct{})
	slow := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		select {
		case <-done:
		case <-r.Context().Done():
		}
	}))
	t.Cleanup(slow.Close)
	t.Cleanup(func() { close(done) })

	cfg := httpCfg()
	cfg.UpstreamTimeout = 50 * time.Millisecond
	code, body := do(t, newProxy(t, cfg), map[string]string{headers.TargetHost: slow.Listener.Addr().String()})
	if code != http.StatusGatewayTimeout || !strings.Contains(body, "upstream_timeout") {
		t.Fatalf("status=%d body=%s", code, body)
	}
}

func TestUpstreamDialFailure502(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	_ = l.Close() // nothing listens here any more

	code, body := do(t, newProxy(t, httpCfg()), map[string]string{headers.TargetHost: addr})
	if code != http.StatusBadGateway || !strings.Contains(body, "bad_gateway") {
		t.Fatalf("status=%d body=%s", code, body)
	}
}

func TestSSRFGuardBlocksResolvedPrivateIP(t *testing.T) {
	up := okUpstream(t, nil)
	port := up.Listener.Addr().String()
	port = port[strings.LastIndex(port, ":")+1:]

	cfg := httpCfg()
	cfg.UpstreamAllowPrivate = false
	url := newProxy(t, cfg)
	// localhost is a name, not an IP literal: only a resolved-IP check catches it.
	for _, host := range []string{"localhost:" + port, "127.0.0.1:" + port, "[::1]:" + port, "169.254.169.254:" + port} {
		code, body := do(t, url, map[string]string{headers.TargetHost: host})
		if code != http.StatusForbidden || !strings.Contains(body, "upstream address not allowed") {
			t.Fatalf("%q: status=%d body=%s", host, code, body)
		}
	}

	cfg.UpstreamAllowPrivate = true
	if code, _ := do(t, newProxy(t, cfg), map[string]string{headers.TargetHost: "localhost:" + port}); code != http.StatusOK {
		t.Fatalf("allow private: status=%d", code)
	}
}

func TestUpstreamPortAllowList(t *testing.T) {
	up := okUpstream(t, nil)
	port := up.Listener.Addr().String()
	port = port[strings.LastIndex(port, ":")+1:]
	n, _ := strconv.Atoi(port)

	cfg := httpCfg()
	cfg.UpstreamAllowPorts = []int{80, 443}
	url := newProxy(t, cfg)
	for _, host := range []string{"localhost:" + port, "localhost:8443", "localhost:22"} {
		code, body := do(t, url, map[string]string{headers.TargetHost: host})
		if code != http.StatusForbidden || !strings.Contains(body, "upstream port not allowed") {
			t.Fatalf("%q: status=%d body=%s", host, code, body)
		}
	}

	cfg.UpstreamAllowPorts = []int{n}
	if code, _ := do(t, newProxy(t, cfg), map[string]string{headers.TargetHost: "localhost:" + port}); code != http.StatusOK {
		t.Fatalf("listed port: status=%d", code)
	}

	// Implicit port: https -> 443, http -> 80. Neither is listed, so both are refused
	// before any dial.
	cfg.UpstreamAllowPorts = []int{n}
	url = newProxy(t, cfg)
	for _, scheme := range []string{"http", "https"} {
		code, body := do(t, url, map[string]string{headers.TargetHost: "localhost", headers.TargetScheme: scheme})
		if code != http.StatusForbidden || !strings.Contains(body, "upstream port not allowed") {
			t.Fatalf("implicit %s: status=%d body=%s", scheme, code, body)
		}
	}
}
