package metrics_test

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"log/slog"
	"math/rand"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/denisitpro/lux-l7/internal/auth"
	"github.com/denisitpro/lux-l7/internal/config"
	"github.com/denisitpro/lux-l7/internal/headers"
	"github.com/denisitpro/lux-l7/internal/metrics"
	"github.com/denisitpro/lux-l7/internal/policy"
	"github.com/denisitpro/lux-l7/internal/proxy"
	"github.com/denisitpro/lux-l7/internal/relay"
	"github.com/denisitpro/lux-l7/internal/route"
	"github.com/denisitpro/lux-l7/internal/tunnel"
)

var discard = slog.New(slog.DiscardHandler)

// scrape returns the exposition text of reg, served through the real handler.
func scrape(t *testing.T, reg *prometheus.Registry) string {
	t.Helper()
	rec := httptest.NewRecorder()
	metrics.Handler(reg).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("scrape status %d", rec.Code)
	}
	return rec.Body.String()
}

func wantLine(t *testing.T, body, line string) {
	t.Helper()
	for _, l := range strings.Split(body, "\n") {
		if l == line {
			return
		}
	}
	t.Fatalf("missing line %q in:\n%s", line, grep(body, "lux_l7_"))
}

func grep(body, prefix string) string {
	var out []string
	for _, l := range strings.Split(body, "\n") {
		if strings.HasPrefix(l, prefix) {
			out = append(out, l)
		}
	}
	return strings.Join(out, "\n")
}

func newMetrics(opts metrics.Options) (*prometheus.Registry, *metrics.Metrics) {
	reg := metrics.NewRegistry()
	return reg, metrics.New(reg, opts)
}

func get(t *testing.T, url, host string, hdr map[string]string) int {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, url, nil)
	if host != "" {
		req.Host = host
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, res.Body)
	_ = res.Body.Close()
	return res.StatusCode
}

func TestHeaderModeCounters(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("ok")) }))
	t.Cleanup(up.Close)
	deadAddr := func() string {
		l, _ := net.Listen("tcp", "127.0.0.1:0")
		a := l.Addr().String()
		_ = l.Close()
		return a
	}()

	reg, m := newMetrics(metrics.Options{Location: "eu-1", IngressHosts: []string{"Front.Example.com"}})
	cfg := config.Config{
		UpstreamScheme: "http", UpstreamAllowHTTP: true, UpstreamAllowPrivate: true,
		UpstreamTimeout: 2 * time.Second,
		AllowHosts:      []string{"127.0.0.1"},
		AllowSuffixes:   []string{"example.com"},
	}
	v, _ := auth.New(cfg)
	srv := httptest.NewServer(proxy.New(cfg, v, discard, proxy.WithMetrics(m)))
	t.Cleanup(srv.Close)

	target := func(a string) map[string]string {
		return map[string]string{headers.TargetHost: a, headers.TargetScheme: "http"}
	}
	if c := get(t, srv.URL, "front.example.com:8080", target(up.Listener.Addr().String())); c != 200 {
		t.Fatalf("success status %d", c)
	}
	if c := get(t, srv.URL, "random.invalid", target("evil.net")); c != 403 {
		t.Fatalf("forbidden status %d", c)
	}
	if c := get(t, srv.URL, "", nil); c != 400 {
		t.Fatalf("bad_target status %d", c)
	}
	if c := get(t, srv.URL, "front.example.com", target(deadAddr)); c != 502 {
		t.Fatalf("upstream error status %d", c)
	}

	body := scrape(t, reg)
	wantLine(t, body, `lux_l7_requests_total{ingress_host="front.example.com",location="eu-1",mode="header",status_class="2xx",upstream_host="127.0.0.1"} 1`)
	wantLine(t, body, `lux_l7_requests_total{ingress_host="other",location="eu-1",mode="header",status_class="4xx",upstream_host="other"} 2`)
	wantLine(t, body, `lux_l7_requests_total{ingress_host="front.example.com",location="eu-1",mode="header",status_class="5xx",upstream_host="127.0.0.1"} 1`)
	wantLine(t, body, `lux_l7_rejections_total{location="eu-1",mode="header",reason="forbidden_host"} 1`)
	wantLine(t, body, `lux_l7_rejections_total{location="eu-1",mode="header",reason="bad_target"} 1`)
	wantLine(t, body, `lux_l7_upstream_errors_total{location="eu-1",mode="header",reason="other"} 1`)
	if !strings.Contains(body, `lux_l7_request_duration_seconds_count{location="eu-1",mode="header",status_class="2xx"} 1`) {
		t.Fatalf("duration histogram missing:\n%s", grep(body, "lux_l7_request_duration_seconds_count"))
	}
}

func TestRouteModeCounters(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("ok")) }))
	t.Cleanup(up.Close)
	l, _ := net.Listen("tcp", "127.0.0.1:0")
	dead := l.Addr().String()
	_ = l.Close()

	routes, err := route.ParseJSON(`[
		{"host":"example.com","path_prefix":"/ok","upstream":"http://` + up.Listener.Addr().String() + `"},
		{"host":".example.net","path_prefix":"/dead","upstream":"http://` + dead + `"}
	]`)
	if err != nil {
		t.Fatal(err)
	}
	reg, m := newMetrics(metrics.Options{})
	cfg := config.Config{UpstreamMaxBytes: 1 << 20, RouteMaxHops: 1, UpstreamTimeout: 2 * time.Second}
	srv := httptest.NewServer(relay.New(cfg, routes, discard, relay.Options{Metrics: m}))
	t.Cleanup(srv.Close)

	if c := get(t, srv.URL+"/ok", "example.com", nil); c != 200 {
		t.Fatalf("success %d", c)
	}
	if c := get(t, srv.URL+"/nope", "unknown.test", nil); c != 404 {
		t.Fatalf("no_route %d", c)
	}
	if c := get(t, srv.URL+"/dead", "a.example.net", nil); c != 502 {
		t.Fatalf("upstream error %d", c)
	}

	body := scrape(t, reg)
	wantLine(t, body, `lux_l7_requests_total{ingress_host="example.com",mode="route",status_class="2xx",upstream_host="127.0.0.1"} 1`)
	wantLine(t, body, `lux_l7_requests_total{ingress_host="other",mode="route",status_class="4xx",upstream_host="other"} 1`)
	wantLine(t, body, `lux_l7_requests_total{ingress_host=".example.net",mode="route",status_class="5xx",upstream_host="127.0.0.1"} 1`)
	wantLine(t, body, `lux_l7_rejections_total{mode="route",reason="no_route"} 1`)
	wantLine(t, body, `lux_l7_upstream_errors_total{mode="route",reason="other"} 1`)
	if strings.Contains(body, "location=") {
		t.Fatal("no location label expected when METRICS_LOCATION is empty")
	}
}

func TestTunnelCounters(t *testing.T) {
	echo, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = echo.Close() })
	go func() {
		for {
			c, err := echo.Accept()
			if err != nil {
				return
			}
			go func() { defer func() { _ = c.Close() }(); _, _ = io.Copy(c, c) }()
		}
	}()
	port := echo.Addr().(*net.TCPAddr).Port

	reg, m := newMetrics(metrics.Options{IngressHosts: []string{"127.0.0.1"}})
	cfg := config.Config{
		UpstreamAllowPrivate: true, UpstreamTimeout: 2 * time.Second, ReadHeaderTimeout: 2 * time.Second,
		AllowHosts: []string{"127.0.0.1"}, TunnelAllowPorts: []int{port}, TunnelIdleTimeout: 5 * time.Second,
	}
	v, _ := auth.New(cfg)
	ts, err := tunnel.New(cfg, v, discard, tunnel.WithMetrics(m))
	if err != nil {
		t.Fatal(err)
	}
	l, _ := net.Listen("tcp", "127.0.0.1:0")
	go func() { _ = ts.Serve(l) }()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
		defer cancel()
		_ = ts.Shutdown(ctx)
	})

	connect := func(target string) (net.Conn, string) {
		c, err := net.Dial("tcp", l.Addr().String())
		if err != nil {
			t.Fatal(err)
		}
		_ = c.SetDeadline(time.Now().Add(5 * time.Second))
		_, _ = fmt.Fprintf(c, "CONNECT %s HTTP/1.1\r\nHost: %s\r\n\r\n", target, target)
		line, _ := bufio.NewReader(c).ReadString('\n')
		return c, strings.TrimSpace(line)
	}

	if _, line := connect("evil.net:" + fmt.Sprint(port)); !strings.Contains(line, "403") {
		t.Fatalf("forbidden: %q", line)
	}
	c, line := connect(fmt.Sprintf("127.0.0.1:%d", port))
	if !strings.Contains(line, "200") {
		t.Fatalf("open: %q", line)
	}
	waitFor(t, func() bool { return strings.Contains(scrape(t, reg), "lux_l7_tunnels_active 1") }, "tunnels_active 1")
	body := scrape(t, reg)
	// An open tunnel is counted at once, before it closes.
	wantLine(t, body, `lux_l7_requests_total{ingress_host="other",mode="tunnel",status_class="2xx",upstream_host="127.0.0.1"} 1`)
	wantLine(t, body, `lux_l7_requests_total{ingress_host="other",mode="tunnel",status_class="4xx",upstream_host="other"} 1`)
	wantLine(t, body, `lux_l7_rejections_total{mode="tunnel",reason="forbidden_host"} 1`)

	_ = c.Close()
	waitFor(t, func() bool { return strings.Contains(scrape(t, reg), "lux_l7_tunnels_active 0") }, "tunnels_active 0")
	wantLine(t, scrape(t, reg), `lux_l7_requests_total{ingress_host="other",mode="tunnel",status_class="2xx",upstream_host="127.0.0.1"} 1`)
}

func waitFor(t *testing.T, cond func() bool, what string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timeout waiting for %s", what)
}

// A thousand arbitrary client-chosen hosts must collapse to a handful of series.
func TestUnknownHostsCollapseToOther(t *testing.T) {
	reg, m := newMetrics(metrics.Options{IngressHosts: []string{"front.example.com"}})
	p := policy.New([]string{"api.example.com"}, []string{"example.org"}, nil)
	rng := rand.New(rand.NewSource(1))
	rnd := func(suffix string) string { return fmt.Sprintf("h%d-%d%s", rng.Int63(), rng.Int63(), suffix) }

	for i := 0; i < 1000; i++ {
		w, obs := m.Begin(metrics.ModeHeader, httptest.NewRecorder())
		obs.Ingress(rnd(".example.com") + ":8443")
		obs.Upstream(p, rnd(".evil.net"))
		w.WriteHeader(http.StatusOK)
		obs.Done()
	}
	// Known names still resolve; the suffix entry is reported as configured.
	w, obs := m.Begin(metrics.ModeHeader, httptest.NewRecorder())
	obs.Ingress("FRONT.example.com")
	obs.Upstream(p, rnd(".example.org"))
	w.WriteHeader(http.StatusOK)
	obs.Done()

	body := scrape(t, reg)
	series := strings.Split(grep(body, "lux_l7_requests_total{"), "\n")
	if len(series) != 2 {
		t.Fatalf("want 2 series, got %d:\n%v", len(series), series)
	}
	wantLine(t, body, `lux_l7_requests_total{ingress_host="other",mode="header",status_class="2xx",upstream_host="other"} 1000`)
	wantLine(t, body, `lux_l7_requests_total{ingress_host="front.example.com",mode="header",status_class="2xx",upstream_host=".example.org"} 1`)
}

func TestHostsMapping(t *testing.T) {
	h := metrics.NewHosts([]string{"Front.Example.com.", "b.example.com"})
	for in, want := range map[string]string{
		"front.example.com":      "front.example.com",
		"FRONT.example.com:8080": "front.example.com",
		"b.example.com.":         "b.example.com",
		"other.example.com":      "other",
		"":                       "other",
		"[::1]:80":               "other",
	} {
		if got := h.Ingress(in); got != want {
			t.Errorf("Ingress(%q) = %q, want %q", in, got, want)
		}
	}
	if h.RouteIngress("") != "any" || h.RouteIngress(".example.com") != ".example.com" {
		t.Error("RouteIngress")
	}
	if h.Upstream("") != "other" || h.Upstream(".example.org") != ".example.org" {
		t.Error("Upstream")
	}
	if h.RouteUpstream("") != "other" || h.RouteUpstream("origin.example.com") != "origin.example.com" {
		t.Error("RouteUpstream")
	}
}

func TestNilMetricsRecordNothing(t *testing.T) {
	var m *metrics.Metrics
	rec := httptest.NewRecorder()
	w, obs := m.Begin(metrics.ModeHeader, rec)
	if obs != nil || w != http.ResponseWriter(rec) {
		t.Fatal("nil Metrics must pass the writer through")
	}
	obs.Ingress("x")
	obs.Route("x", "y")
	obs.Upstream(policy.New(nil, nil, nil), "x")
	obs.Reject(metrics.RejectNoRoute)
	obs.UpstreamError(metrics.UpstreamOther)
	obs.SetStatus(200)
	obs.TunnelOpen()
	obs.Done()
	if metrics.From(rec) != nil {
		t.Fatal("From on a plain writer must be nil")
	}
}

func TestClientCancelIsErrorClass(t *testing.T) {
	reg, m := newMetrics(metrics.Options{})
	_, obs := m.Begin(metrics.ModeHeader, httptest.NewRecorder())
	proxy.ObserveUpstreamError(obs, context.Canceled)
	obs.Done() // nothing was written
	body := scrape(t, reg)
	wantLine(t, body, `lux_l7_requests_total{ingress_host="other",mode="header",status_class="error",upstream_host="other"} 1`)
	wantLine(t, body, `lux_l7_upstream_errors_total{mode="header",reason="canceled"} 1`)
}

// A body cut by the size limit surfaces as a transport error but is the client's
// rejection, not an upstream failure.
func TestBodyCutIsRejectionNotUpstreamError(t *testing.T) {
	reg, m := newMetrics(metrics.Options{})
	_, obs := m.Begin(metrics.ModeHeader, httptest.NewRecorder())
	proxy.ObserveUpstreamError(obs, fmt.Errorf("transport: %w", &http.MaxBytesError{Limit: 10}))
	obs.Done()
	body := scrape(t, reg)
	wantLine(t, body, `lux_l7_rejections_total{mode="header",reason="body_too_large"} 1`)
	if strings.Contains(body, "lux_l7_upstream_errors_total{") {
		t.Fatalf("unexpected upstream error series:\n%s", grep(body, "lux_l7_upstream"))
	}
}

func TestStartEmptyAddrIsOff(t *testing.T) {
	srv, err := metrics.Start(discard, "", metrics.NewRegistry())
	if err != nil || srv != nil {
		t.Fatalf("got %v, %v; want nil, nil", srv, err)
	}
	if srv.Addr() != nil || srv.Shutdown(context.Background()) != nil {
		t.Fatal("nil server must be inert")
	}
}

func TestStartServesOnlyMetrics(t *testing.T) {
	reg, m := newMetrics(metrics.Options{Location: "eu-1"})
	_, obs := m.Begin(metrics.ModeRoute, httptest.NewRecorder())
	obs.Done()

	srv, err := metrics.Start(discard, "127.0.0.1:0", reg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = srv.Shutdown(context.Background()) })
	base := "http://" + srv.Addr().String()

	res, err := http.Get(base + "/metrics")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	body := string(b)
	if res.StatusCode != 200 || !strings.Contains(body, "lux_l7_requests_total") ||
		!strings.Contains(body, "go_goroutines") || !strings.Contains(body, "process_cpu_seconds_total") {
		t.Fatalf("scrape: %d\n%.400s", res.StatusCode, body)
	}
	res, err = http.Get(base + "/")
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != http.StatusNotFound {
		t.Fatalf("non-metrics path: %d", res.StatusCode)
	}
}

func TestMetricsNotOnProxyListener(t *testing.T) {
	_, m := newMetrics(metrics.Options{})
	cfg := config.Config{}
	v, _ := auth.New(cfg)
	srv := httptest.NewServer(proxy.New(cfg, v, discard, proxy.WithMetrics(m)))
	t.Cleanup(srv.Close)
	if c := get(t, srv.URL+"/metrics", "", nil); c != http.StatusBadRequest {
		t.Fatalf("proxy listener answered /metrics with %d, want the normal bad_target 400", c)
	}
}
