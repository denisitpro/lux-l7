package relay_test

import (
	"bufio"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/denisitpro/lux-l7/internal/config"
	"github.com/denisitpro/lux-l7/internal/headers"
	"github.com/denisitpro/lux-l7/internal/relay"
	"github.com/denisitpro/lux-l7/internal/route"
)

func TestScenarioAHostPreserveAndRewrite(t *testing.T) {
	var seenHost string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenHost = r.Host
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(upstream.Close)

	routes := mustRoutes(t, `[
		{"host":"example.com","path_prefix":"/location1","upstream":"`+upstreamURL(upstream)+`","host_header":"example.com","rewrite_prefix":"/"}
	]`)
	h := relay.New(testCfg(), routes, nil, relay.Options{})
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/location1/x", nil)
	req.Host = "example.com"
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK || seenHost != "example.com" {
		t.Fatalf("status=%d host=%q", res.StatusCode, seenHost)
	}

	routes2 := mustRoutes(t, `[
		{"host":"example.com","path_prefix":"/location1","upstream":"`+upstreamURL(upstream)+`","host_header":"origin.example.com","rewrite_prefix":"/"}
	]`)
	h2 := relay.New(testCfg(), routes2, nil, relay.Options{})
	srv2 := httptest.NewServer(h2)
	t.Cleanup(srv2.Close)

	seenHost = ""
	req2, _ := http.NewRequest(http.MethodGet, srv2.URL+"/location1/y", nil)
	req2.Host = "example.com"
	res2, err := http.DefaultClient.Do(req2)
	if err != nil {
		t.Fatal(err)
	}
	_ = res2.Body.Close()
	if seenHost != "origin.example.com" {
		t.Fatalf("host=%q", seenHost)
	}
}

func TestScenarioBTwoRoutes(t *testing.T) {
	local := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("local"))
	}))
	central := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("central"))
	}))
	t.Cleanup(local.Close)
	t.Cleanup(central.Close)

	routes := mustRoutes(t, `[
		{"host":"example.com","path_prefix":"/local","upstream":"`+upstreamURL(local)+`"},
		{"host":"example.com","path_prefix":"/","upstream":"`+upstreamURL(central)+`"}
	]`)
	h := relay.New(testCfg(), routes, nil, relay.Options{})
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)

	body := get(t, srv.URL+"/local/x", "example.com")
	if body != "local" {
		t.Fatalf("local body=%q", body)
	}
	body = get(t, srv.URL+"/other", "example.com")
	if body != "central" {
		t.Fatalf("central body=%q", body)
	}
}

func TestTLSWithSNIName(t *testing.T) {
	tlsUpstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(tlsUpstream.Close)

	addr := tlsUpstream.Listener.Addr().String()
	pool := x509.NewCertPool()
	pool.AddCert(tlsUpstream.Certificate())

	okRoutes := mustRoutes(t, `[{
		"host":"example.com","path_prefix":"/","upstream":"https://`+addr+`",
		"tls_server_name":"example.com"
	}]`)
	h := relay.New(testCfg(), okRoutes, nil, relay.Options{RootCAs: pool})
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/", nil)
	req.Host = "example.com"
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status=%d", res.StatusCode)
	}

	badRoutes := mustRoutes(t, `[{
		"host":"example.com","path_prefix":"/","upstream":"https://`+addr+`",
		"tls_server_name":"wrong.example.net"
	}]`)
	h2 := relay.New(testCfg(), badRoutes, nil, relay.Options{RootCAs: pool})
	srv2 := httptest.NewServer(h2)
	t.Cleanup(srv2.Close)

	req2, _ := http.NewRequest(http.MethodGet, srv2.URL+"/", nil)
	req2.Host = "example.com"
	res2, err := http.DefaultClient.Do(req2)
	if err != nil {
		t.Fatal(err)
	}
	_ = res2.Body.Close()
	if res2.StatusCode != http.StatusBadGateway {
		t.Fatalf("status=%d", res2.StatusCode)
	}
}

func TestHops(t *testing.T) {
	var seenHops string
	var called bool
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		seenHops = r.Header.Get(headers.Hops)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(upstream.Close)

	routes := mustRoutes(t, `[{"host":"example.com","path_prefix":"/","upstream":"`+upstreamURL(upstream)+`"}]`)
	cfg := testCfg()
	cfg.RouteMaxHops = 1
	h := relay.New(cfg, routes, nil, relay.Options{})
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)

	called = false
	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/", nil)
	req.Host = "example.com"
	res, _ := http.DefaultClient.Do(req)
	_ = res.Body.Close()
	if !called || seenHops != "1" {
		t.Fatalf("called=%v hops=%q", called, seenHops)
	}

	called = false
	req2, _ := http.NewRequest(http.MethodGet, srv.URL+"/", nil)
	req2.Host = "example.com"
	req2.Header.Set(headers.Hops, "1")
	res2, _ := http.DefaultClient.Do(req2)
	body, _ := io.ReadAll(res2.Body)
	_ = res2.Body.Close()
	if called {
		t.Fatal("upstream called on loop")
	}
	if res2.StatusCode != 508 {
		t.Fatalf("status=%d", res2.StatusCode)
	}
	var errBody map[string]string
	_ = json.Unmarshal(body, &errBody)
	if errBody["error"] != "loop_detected" {
		t.Fatalf("body=%s", body)
	}

	req3, _ := http.NewRequest(http.MethodGet, srv.URL+"/", nil)
	req3.Host = "example.com"
	req3.Header.Set(headers.Hops, "nope")
	res3, _ := http.DefaultClient.Do(req3)
	_ = res3.Body.Close()
	if res3.StatusCode != http.StatusBadRequest {
		t.Fatalf("status=%d", res3.StatusCode)
	}
}

func TestForwardedAndStrippedHeaders(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get(headers.TargetHost) != "" || r.Header.Get(headers.ProxyToken) != "" {
			t.Error("routing headers leaked")
		}
		if r.Header.Get("X-Forwarded-Host") != "example.com" {
			t.Fatalf("xff-host=%q", r.Header.Get("X-Forwarded-Host"))
		}
		if r.Header.Get("X-Forwarded-Proto") == "" {
			t.Fatal("missing x-forwarded-proto")
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(upstream.Close)

	routes := mustRoutes(t, `[{"host":"example.com","path_prefix":"/","upstream":"`+upstreamURL(upstream)+`"}]`)
	h := relay.New(testCfg(), routes, nil, relay.Options{})
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/", nil)
	req.Host = "example.com"
	req.Header.Set(headers.TargetHost, "evil")
	req.Header.Set(headers.ProxyToken, "secret")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
}

func TestNoRoute(t *testing.T) {
	routes := mustRoutes(t, `[{"host":"example.com","path_prefix":"/only","upstream":"http://127.0.0.1:1"}]`)
	h := relay.New(testCfg(), routes, nil, relay.Options{})
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/nope", nil)
	req.Host = "other.example.net"
	res, _ := http.DefaultClient.Do(req)
	body, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusNotFound {
		t.Fatalf("status=%d", res.StatusCode)
	}
	if !strings.Contains(string(body), "no_route") {
		t.Fatalf("body=%s", body)
	}
}

func testCfg() config.Config {
	return config.Config{
		UpstreamMaxBytes: 1 << 20,
		RouteMaxHops:     1,
	}
}

func mustRoutes(t *testing.T, raw string) []route.Route {
	r, err := route.ParseJSON(raw)
	if err != nil {
		t.Fatalf("routes: %v", err)
	}
	return r
}

func upstreamURL(srv *httptest.Server) string {
	return "http://" + srv.Listener.Addr().String()
}

func get(t *testing.T, url, host string) string {
	req, _ := http.NewRequest(http.MethodGet, url, nil)
	req.Host = host
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	b, _ := io.ReadAll(res.Body)
	return string(b)
}

func doGet(t *testing.T, url, host string) *http.Response {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, url, nil)
	req.Host = host
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, res.Body)
	_ = res.Body.Close()
	return res
}

func TestTLSDefaultServerNameIsHost(t *testing.T) {
	var sni atomic.Value
	up := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sni.Store(r.TLS.ServerName)
	}))
	t.Cleanup(up.Close)
	pool := x509.NewCertPool()
	pool.AddCert(up.Certificate())

	routes := mustRoutes(t, `[{"host":"example.com","path_prefix":"/","upstream":"https://`+up.Listener.Addr().String()+`","host_header":"example.com"}]`)
	srv := httptest.NewServer(relay.New(testCfg(), routes, nil, relay.Options{RootCAs: pool}))
	t.Cleanup(srv.Close)

	if res := doGet(t, srv.URL+"/", "example.com"); res.StatusCode != http.StatusOK {
		t.Fatalf("status=%d", res.StatusCode)
	}
	if got, _ := sni.Load().(string); got != "example.com" {
		t.Fatalf("sni=%q", got)
	}
}

func TestTLSConnectionReuse(t *testing.T) {
	var conns atomic.Int32
	up := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	up.Config.ConnState = func(_ net.Conn, st http.ConnState) {
		if st == http.StateNew {
			conns.Add(1)
		}
	}
	up.StartTLS()
	t.Cleanup(up.Close)
	pool := x509.NewCertPool()
	pool.AddCert(up.Certificate())

	routes := mustRoutes(t, `[{"host":"example.com","path_prefix":"/","upstream":"https://`+up.Listener.Addr().String()+`"}]`)
	srv := httptest.NewServer(relay.New(testCfg(), routes, nil, relay.Options{RootCAs: pool}))
	t.Cleanup(srv.Close)

	for range 3 {
		if res := doGet(t, srv.URL+"/", "example.com"); res.StatusCode != http.StatusOK {
			t.Fatalf("status=%d", res.StatusCode)
		}
	}
	if n := conns.Load(); n != 1 {
		t.Fatalf("upstream connections=%d want 1", n)
	}
}

func selfSignedCert(t *testing.T, names ...string) tls.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: names[0]},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:              names,
		IPAddresses:           []net.IP{net.ParseIP("127.0.0.1")},
		IsCA:                  true,
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
}

func TestSharedUpstreamTwoNamesSNIMatchesHost(t *testing.T) {
	cert := selfSignedCert(t, "a.example.com", "b.example.com")
	var mu sync.Mutex
	var bad []string
	up := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host, _, err := net.SplitHostPort(r.Host)
		if err != nil {
			host = r.Host
		}
		if r.TLS.ServerName != host {
			mu.Lock()
			bad = append(bad, r.TLS.ServerName+" != "+host)
			mu.Unlock()
		}
		w.WriteHeader(http.StatusOK)
	}))
	up.TLS = &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12}
	up.StartTLS()
	t.Cleanup(up.Close)
	leaf, err := x509.ParseCertificate(cert.Certificate[0])
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	pool.AddCert(leaf)

	addr := up.Listener.Addr().String()
	routes := mustRoutes(t, `[
		{"host":"a.test.example.net","path_prefix":"/","upstream":"https://`+addr+`","host_header":"a.example.com"},
		{"host":"b.test.example.net","path_prefix":"/","upstream":"https://`+addr+`","host_header":"b.example.com"}
	]`)
	srv := httptest.NewServer(relay.New(testCfg(), routes, nil, relay.Options{RootCAs: pool}))
	t.Cleanup(srv.Close)

	for _, h := range []string{"a.test.example.net", "b.test.example.net", "a.test.example.net", "b.test.example.net"} {
		if res := doGet(t, srv.URL+"/", h); res.StatusCode != http.StatusOK {
			t.Fatalf("host=%s status=%d", h, res.StatusCode)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if len(bad) > 0 {
		t.Fatalf("SNI/Host mismatch: %v", bad)
	}
}

func TestEncodedPathPreserved(t *testing.T) {
	var uri atomic.Value
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		uri.Store(r.RequestURI)
	}))
	t.Cleanup(up.Close)

	cases := []struct{ rewrite, want string }{
		{``, "/location1/a%2Fb"},
		{`,"rewrite_prefix":"/"`, "/a%2Fb"},
	}
	for _, c := range cases {
		routes := mustRoutes(t, `[{"host":"example.com","path_prefix":"/location1","upstream":"`+upstreamURL(up)+`"`+c.rewrite+`}]`)
		srv := httptest.NewServer(relay.New(testCfg(), routes, nil, relay.Options{}))
		doGet(t, srv.URL+"/location1/a%2Fb", "example.com")
		srv.Close()
		if got, _ := uri.Load().(string); got != c.want {
			t.Fatalf("rewrite %q: RequestURI=%q want %q", c.rewrite, got, c.want)
		}
	}
}

func TestConnectRejected(t *testing.T) {
	var called atomic.Bool
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called.Store(true)
	}))
	t.Cleanup(up.Close)
	routes := mustRoutes(t, `[{"host":"example.com","path_prefix":"/","upstream":"`+upstreamURL(up)+`"}]`)
	srv := httptest.NewServer(relay.New(testCfg(), routes, nil, relay.Options{}))
	t.Cleanup(srv.Close)

	req, _ := http.NewRequest(http.MethodConnect, srv.URL, nil)
	req.Host = "example.com"
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusMethodNotAllowed || !strings.Contains(string(body), "method_not_allowed") {
		t.Fatalf("status=%d body=%s", res.StatusCode, body)
	}
	if called.Load() {
		t.Fatal("upstream called")
	}
}

func TestForwardedProtoIsProxyOwn(t *testing.T) {
	var seen string
	upstream := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		seen = r.Header.Get("X-Forwarded-Proto")
	}))
	t.Cleanup(upstream.Close)
	routes := mustRoutes(t, `[{"host":"example.com","path_prefix":"/","upstream":"`+upstreamURL(upstream)+`"}]`)
	h := relay.New(testCfg(), routes, nil, relay.Options{})

	plain := httptest.NewServer(h)
	t.Cleanup(plain.Close)
	req, _ := http.NewRequest(http.MethodGet, plain.URL+"/", nil)
	req.Host = "example.com"
	req.Header.Set("X-Forwarded-Proto", "https")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if seen != "http" {
		t.Fatalf("plain client connection: X-Forwarded-Proto=%q, want http", seen)
	}

	secure := httptest.NewTLSServer(h)
	t.Cleanup(secure.Close)
	req, _ = http.NewRequest(http.MethodGet, secure.URL+"/", nil)
	req.Host = "example.com"
	req.Header.Set("X-Forwarded-Proto", "http")
	res, err = secure.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if seen != "https" {
		t.Fatalf("TLS client connection: X-Forwarded-Proto=%q, want https", seen)
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

	cfg := testCfg()
	cfg.UpstreamTimeout = 50 * time.Millisecond
	routes := mustRoutes(t, `[{"host":"example.com","path_prefix":"/","upstream":"`+upstreamURL(slow)+`"}]`)
	srv := httptest.NewServer(relay.New(cfg, routes, nil, relay.Options{}))
	t.Cleanup(srv.Close)

	res := doGet(t, srv.URL+"/", "example.com")
	if res.StatusCode != http.StatusGatewayTimeout {
		t.Fatalf("status=%d", res.StatusCode)
	}
}

func TestDefaultHostIsUpstreamAuthority(t *testing.T) {
	var seenHost, seenFwd string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenHost, seenFwd = r.Host, r.Header.Get("X-Forwarded-Host")
	}))
	t.Cleanup(upstream.Close)

	routes := mustRoutes(t, `[{"host":".example.com","path_prefix":"/","upstream":"`+upstreamURL(upstream)+`"}]`)
	srv := httptest.NewServer(relay.New(testCfg(), routes, nil, relay.Options{}))
	t.Cleanup(srv.Close)

	if res := doGet(t, srv.URL+"/", "a.example.com"); res.StatusCode != http.StatusOK {
		t.Fatalf("status=%d", res.StatusCode)
	}
	if seenHost != upstream.Listener.Addr().String() || seenFwd != "a.example.com" {
		t.Fatalf("host=%q fwd=%q", seenHost, seenFwd)
	}
}

func TestChunkedBodyOverLimit413(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
	}))
	t.Cleanup(up.Close)
	cfg := testCfg()
	cfg.UpstreamMaxBytes = 8
	srv := httptest.NewServer(relay.New(cfg, mustRoutes(t, `[{"host":"example.com","path_prefix":"/","upstream":"`+upstreamURL(up)+`"}]`), nil, relay.Options{}))
	t.Cleanup(srv.Close)

	// io.MultiReader hides the length, so the request goes out chunked.
	body := io.MultiReader(strings.NewReader("0123456789"), strings.NewReader("abcdef"))
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/", body)
	req.Host = "example.com"
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	b, _ := io.ReadAll(res.Body)
	if res.StatusCode != http.StatusRequestEntityTooLarge || !strings.Contains(string(b), "body_too_large") {
		t.Fatalf("status=%d body=%q", res.StatusCode, b)
	}
}

func TestSSEStreamsThroughRelay(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		for i := 0; i < 5; i++ {
			_, _ = w.Write([]byte("data: tick\n\n"))
			w.(http.Flusher).Flush()
			time.Sleep(100 * time.Millisecond)
		}
	}))
	t.Cleanup(up.Close)
	srv := httptest.NewServer(relay.New(testCfg(), mustRoutes(t, `[{"host":"example.com","path_prefix":"/","upstream":"`+upstreamURL(up)+`"}]`), nil, relay.Options{}))
	t.Cleanup(srv.Close)

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/", nil)
	req.Host = "example.com"
	start := time.Now()
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	br := bufio.NewReader(res.Body)
	if line, err := br.ReadString('\n'); err != nil || line != "data: tick\n" {
		t.Fatalf("first event %q err=%v", line, err)
	}
	if time.Since(start) > 300*time.Millisecond {
		t.Fatal("first event was buffered until the stream ended")
	}
	rest, _ := io.ReadAll(br)
	if got := strings.Count(string(rest), "data: tick"); got != 4 {
		t.Fatalf("remaining events = %d, want 4", got)
	}
}

func TestWebSocketUpgradeThroughRelay(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Upgrade") != "websocket" {
			t.Errorf("Upgrade header = %q", r.Header.Get("Upgrade"))
		}
		conn, rw, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Error(err)
			return
		}
		defer func() { _ = conn.Close() }()
		_, _ = rw.WriteString("HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n\r\n")
		_ = rw.Flush()
		buf := make([]byte, 4)
		if _, err := io.ReadFull(rw, buf); err == nil {
			_, _ = conn.Write(append([]byte("echo:"), buf...))
		}
	}))
	t.Cleanup(up.Close)
	srv := httptest.NewServer(relay.New(testCfg(), mustRoutes(t, `[{"host":"example.com","path_prefix":"/","upstream":"`+upstreamURL(up)+`"}]`), nil, relay.Options{}))
	t.Cleanup(srv.Close)

	conn, err := net.Dial("tcp", strings.TrimPrefix(srv.URL, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	_, _ = conn.Write([]byte("GET / HTTP/1.1\r\nHost: example.com\r\nConnection: Upgrade\r\nUpgrade: websocket\r\n\r\n"))
	br := bufio.NewReader(conn)
	res, err := http.ReadResponse(br, nil)
	if err != nil || res.StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("handshake: res=%v err=%v", res, err)
	}
	_, _ = conn.Write([]byte("ping"))
	got := make([]byte, len("echo:ping"))
	if _, err := io.ReadFull(br, got); err != nil || string(got) != "echo:ping" {
		t.Fatalf("echo %q err=%v", got, err)
	}
}
