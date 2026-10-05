package relay_test

import (
	"crypto/x509"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

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
		{"host":"example.com","path_prefix":"/location1","upstream":"`+upstreamURL(upstream)+`","rewrite_prefix":"/"}
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
