package reqlog_test

import (
	"bufio"
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/denisitpro/lux-l7/internal/auth"
	"github.com/denisitpro/lux-l7/internal/config"
	"github.com/denisitpro/lux-l7/internal/headers"
	"github.com/denisitpro/lux-l7/internal/metrics"
	"github.com/denisitpro/lux-l7/internal/proxy"
	"github.com/denisitpro/lux-l7/internal/relay"
	"github.com/denisitpro/lux-l7/internal/reqlog"
	"github.com/denisitpro/lux-l7/internal/route"
	"github.com/denisitpro/lux-l7/internal/tunnel"
)

// The tests below drive the real handlers. The client is 127.0.0.1 (httptest), the
// upstream is named localhost, so each can be told apart in the captured output.
const (
	secretToken = "tok-secret-value"
	secretQuery = "token=SECRETQ"
	secretPath  = "/secret/path"
)

// observer wires the request log, plus Prometheus when withMetrics, into one Observer.
func observer(l *reqlog.Logger, withMetrics bool) *metrics.Observer {
	sinks := []metrics.Sink{l}
	if withMetrics {
		sinks = append(sinks, metrics.New(metrics.NewRegistry(), metrics.Options{}))
	}
	return metrics.NewObserver(sinks...)
}

// wantAbsent fails when any of the strings appears in the captured output.
func wantAbsent(t *testing.T, out *syncBuffer, banned ...string) {
	t.Helper()
	for _, b := range banned {
		if strings.Contains(out.String(), b) {
			t.Fatalf("%q leaked into the log:\n%s", b, out.String())
		}
	}
}

func deadAddr(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	a := l.Addr().String()
	_ = l.Close()
	return a
}

func port(addr string) string {
	_, p, _ := net.SplitHostPort(addr)
	return p
}

// waitLines polls until the log holds at least n lines.
func waitLines(t *testing.T, out *syncBuffer, n int) []map[string]any {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if got := lines(t, out); len(got) >= n {
			return got
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timeout waiting for %d log lines, have:\n%s", n, out.String())
	return nil
}

// headerServer runs header mode with token auth and one allowed upstream name.
func headerServer(t *testing.T, level slog.Level, withMetrics bool) (*httptest.Server, *reqlog.Logger, *syncBuffer, string) {
	t.Helper()
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("ok")) }))
	t.Cleanup(up.Close)
	l, out := newLogger(level)
	cfg := config.Config{
		UpstreamScheme: "http", UpstreamAllowHTTP: true, UpstreamAllowPrivate: true,
		UpstreamTimeout: 2 * time.Second, AllowHosts: []string{"localhost"},
		AuthEnabled: true, AuthMode: config.AuthModeInstance, InstanceToken: secretToken,
	}
	v, err := auth.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(proxy.New(cfg, v, slog.New(slog.DiscardHandler), proxy.WithObserver(observer(l, withMetrics))))
	t.Cleanup(srv.Close)
	return srv, l, out, port(up.Listener.Addr().String())
}

// headerTraffic sends an ok request, a forbidden host, a bad token, a missing target
// and a request to a dead upstream (the only one that is a failure).
func headerTraffic(t *testing.T, srv *httptest.Server, upPort string) {
	t.Helper()
	send := func(path string, hdr map[string]string) {
		req, _ := http.NewRequest(http.MethodGet, srv.URL+path+"?"+secretQuery, nil)
		for k, v := range hdr {
			req.Header.Set(k, v)
		}
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = io.Copy(io.Discard, res.Body)
		_ = res.Body.Close()
	}
	target := func(host string) map[string]string {
		return map[string]string{headers.TargetHost: host, headers.TargetScheme: "http", headers.ProxyToken: secretToken}
	}
	send(secretPath, target("localhost:"+upPort))
	send(secretPath, target("evil.net"))
	send(secretPath, map[string]string{headers.TargetHost: "localhost:" + upPort, headers.TargetScheme: "http", headers.ProxyToken: "wrong"})
	send(secretPath, map[string]string{headers.ProxyToken: secretToken})
	send(secretPath, target("localhost:"+port(deadAddr(t))))
}

func TestHeaderModeInfoLeaksNothing(t *testing.T) {
	for _, withMetrics := range []bool{false, true} { // logging does not depend on Prometheus
		srv, l, out, upPort := headerServer(t, slog.LevelInfo, withMetrics)
		headerTraffic(t, srv, upPort)
		headerTraffic(t, srv, upPort)

		// Ten requests, two upstream failures of one reason: one line, reason only.
		got := lines(t, out)
		if len(got) != 1 || got[0]["msg"] != "upstream error" || got[0]["level"] != "ERROR" ||
			got[0]["mode"] != "header" || got[0]["reason"] != "other" || len(got[0]) != 5 {
			t.Fatalf("metrics=%v: want the first upstream error only:\n%s", withMetrics, out.String())
		}
		wantAbsent(t, out, "127.0.0.1", "localhost", "secret", "SECRETQ", secretToken, "evil.net", "/", upPort)

		l.Flush()
		got = lines(t, out)
		if len(got) != 2 || got[1]["msg"] != "upstream errors repeated" || got[1]["count"] != float64(1) {
			t.Fatalf("metrics=%v: want a count line for the repeat:\n%s", withMetrics, out.String())
		}
		wantAbsent(t, out, "127.0.0.1", "localhost", "secret", "SECRETQ", secretToken)
	}
}

func TestHeaderModeWarnReportsReasonOnly(t *testing.T) {
	srv, _, out, upPort := headerServer(t, slog.LevelWarn, false)
	headerTraffic(t, srv, upPort)
	got := lines(t, out)
	if len(got) != 1 || got[0]["reason"] != "other" {
		t.Fatalf("got:\n%s", out.String())
	}
}

func TestHeaderModeDebugOneLinePerRequest(t *testing.T) {
	srv, _, out, upPort := headerServer(t, slog.LevelDebug, false)
	headerTraffic(t, srv, upPort)

	got := lines(t, out) // 5 requests + the first upstream error
	if len(got) != 6 {
		t.Fatalf("want 5 request lines and 1 error line:\n%s", out.String())
	}
	first := got[0]
	if first["msg"] != "request" || first["mode"] != "header" || first["method"] != "GET" ||
		first["target"] != "localhost:"+upPort || first["status"] != float64(200) || first["bytes"] != float64(2) {
		t.Fatalf("ok request line: %v", first)
	}
	if _, ok := first["duration_ms"].(float64); !ok {
		t.Fatalf("no duration: %v", first)
	}
	if got[1]["status"] != float64(403) || got[2]["status"] != float64(401) || got[3]["status"] != float64(400) {
		t.Fatalf("rejection statuses: %v %v %v", got[1], got[2], got[3])
	}
	wantAbsent(t, out, "127.0.0.1", "secret", "SECRETQ", secretToken)
}

// routeServer runs route mode over two routes; route 1 fails while *fail is set.
func routeServer(t *testing.T, level slog.Level, withMetrics bool, fail *atomic.Bool) (*httptest.Server, *reqlog.Logger, *syncBuffer) {
	t.Helper()
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("ok")) }))
	t.Cleanup(up.Close)
	flaky := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if fail.Load() {
			conn, _, _ := w.(http.Hijacker).Hijack()
			_ = conn.Close() // a transport error for the proxy
			return
		}
		_, _ = w.Write([]byte("ok"))
	}))
	t.Cleanup(flaky.Close)
	routes, err := route.ParseJSON(`[
		{"host":"example.com","path_prefix":"/ok","upstream":"http://localhost:` + port(up.Listener.Addr().String()) + `"},
		{"host":"example.com","path_prefix":"/flaky","upstream":"http://localhost:` + port(flaky.Listener.Addr().String()) + `"}
	]`)
	if err != nil {
		t.Fatal(err)
	}
	l, out := newLogger(level)
	cfg := config.Config{UpstreamMaxBytes: 1 << 20, RouteMaxHops: 1, UpstreamTimeout: 2 * time.Second}
	srv := httptest.NewServer(relay.New(cfg, routes, slog.New(slog.DiscardHandler), relay.Options{Observer: observer(l, withMetrics)}))
	t.Cleanup(srv.Close)
	return srv, l, out
}

func routeGet(t *testing.T, srv *httptest.Server, path string) int {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, srv.URL+path+"?"+secretQuery, nil)
	req.Host = "example.com"
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, res.Body)
	_ = res.Body.Close()
	return res.StatusCode
}

func TestRouteModeInfoSummaryPerInterval(t *testing.T) {
	for _, withMetrics := range []bool{false, true} {
		var fail atomic.Bool
		fail.Store(true)
		srv, l, out := routeServer(t, slog.LevelInfo, withMetrics, &fail)
		l.Flush()
		wantNoLines(t, out) // idle: silent

		for range 3 {
			routeGet(t, srv, "/ok"+secretPath)
		}
		routeGet(t, srv, "/flaky"+secretPath)
		routeGet(t, srv, "/flaky"+secretPath)
		routeGet(t, srv, "/nope") // no route: nothing to attribute
		wantNoLines(t, out)       // nothing between flushes

		l.Flush()
		got := lines(t, out)
		if len(got) != 2 {
			t.Fatalf("metrics=%v: want a summary per route with traffic:\n%s", withMetrics, out.String())
		}
		ok, flaky := got[0], got[1]
		if ok["msg"] != "route summary" || ok["route"] != float64(0) || ok["upstream"] != "localhost" ||
			ok["requests"] != float64(3) || ok["status"].(map[string]any)["2xx"] != float64(3) {
			t.Fatalf("route 0: %v", ok)
		}
		if _, has := ok["errors"]; has {
			t.Fatalf("route 0 has no errors: %v", ok)
		}
		if flaky["route"] != float64(1) || flaky["requests"] != float64(2) ||
			flaky["status"].(map[string]any)["5xx"] != float64(2) || flaky["errors"].(map[string]any)["other"] != float64(2) {
			t.Fatalf("route 1: %v", flaky)
		}
		for _, k := range []string{"p50_ms", "p95_ms"} {
			if _, ok := ok[k].(float64); !ok {
				t.Fatalf("missing %s: %v", k, ok)
			}
		}
		wantAbsent(t, out, "127.0.0.1", "secret", "SECRETQ", "/ok", "/flaky")

		l.Flush()
		if n := len(lines(t, out)); n != 2 {
			t.Fatalf("an idle interval must be silent:\n%s", out.String())
		}
	}
}

func TestRouteModeWarnTransitions(t *testing.T) {
	var fail atomic.Bool
	fail.Store(true)
	srv, _, out := routeServer(t, slog.LevelWarn, false, &fail)

	for i := 0; i < reqlog.FailingAfter-1; i++ {
		routeGet(t, srv, "/flaky")
	}
	wantNoLines(t, out)
	routeGet(t, srv, "/flaky")
	for range 3 {
		routeGet(t, srv, "/flaky") // collapsed
	}
	got := lines(t, out)
	if len(got) != 1 || got[0]["msg"] != "upstream failing" || got[0]["route"] != float64(1) ||
		got[0]["upstream"] != "localhost" || got[0]["reason"] != "other" ||
		got[0]["consecutive_errors"] != float64(reqlog.FailingAfter) {
		t.Fatalf("failing:\n%s", out.String())
	}

	fail.Store(false)
	routeGet(t, srv, "/flaky")
	routeGet(t, srv, "/flaky")
	got = lines(t, out)
	if len(got) != 2 || got[1]["msg"] != "upstream recovered" || got[1]["consecutive_errors"] != float64(reqlog.FailingAfter+3) {
		t.Fatalf("recovered:\n%s", out.String())
	}
	wantAbsent(t, out, "127.0.0.1", "SECRETQ", "/flaky")
}

func TestRouteModeDebugOneLinePerRequest(t *testing.T) {
	var fail atomic.Bool
	srv, _, out := routeServer(t, slog.LevelDebug, false, &fail)
	routeGet(t, srv, "/ok"+secretPath)
	routeGet(t, srv, "/nope")
	got := lines(t, out)
	if len(got) != 1 { // the unmatched request has no route to report
		t.Fatalf("want one line:\n%s", out.String())
	}
	if got[0]["msg"] != "request" || got[0]["mode"] != "route" || got[0]["route"] != float64(0) ||
		got[0]["upstream"] != "localhost" || got[0]["status"] != float64(200) {
		t.Fatalf("line: %v", got[0])
	}
	wantAbsent(t, out, "127.0.0.1", "secret", "SECRETQ", "/ok")
}

// tunnelServer runs the CONNECT listener with token auth in front of an echo server.
func tunnelServer(t *testing.T, level slog.Level) (addr string, out *syncBuffer, echoPort, dead string, l *reqlog.Logger) {
	t.Helper()
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
	echoPort = port(echo.Addr().String())
	dead = port(deadAddr(t))

	l, out = newLogger(level)
	cfg := config.Config{
		UpstreamAllowPrivate: true, UpstreamTimeout: 2 * time.Second, ReadHeaderTimeout: 2 * time.Second,
		AllowHosts: []string{"localhost"}, TunnelAllowPorts: mustPorts(echoPort, dead), TunnelIdleTimeout: 5 * time.Second,
		AuthEnabled: true, AuthMode: config.AuthModeInstance, InstanceToken: secretToken,
	}
	v, err := auth.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	ts, err := tunnel.New(cfg, v, slog.New(slog.DiscardHandler), tunnel.WithObserver(observer(l, false)))
	if err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = ts.Serve(ln) }()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
		defer cancel()
		_ = ts.Shutdown(ctx)
	})
	return ln.Addr().String(), out, echoPort, dead, l
}

func mustPorts(ps ...string) []int {
	var res []int
	for _, p := range ps {
		var n int
		_, _ = fmt.Sscan(p, &n)
		res = append(res, n)
	}
	return res
}

// connect sends CONNECT target and returns the conn and the status line.
func connect(t *testing.T, addr, target, token string) (net.Conn, *bufio.Reader, string) {
	t.Helper()
	c, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	_ = c.SetDeadline(time.Now().Add(5 * time.Second))
	cred := base64.StdEncoding.EncodeToString([]byte("user:" + token))
	_, _ = fmt.Fprintf(c, "CONNECT %s HTTP/1.1\r\nHost: %s\r\nProxy-Authorization: Basic %s\r\n\r\n", target, target, cred)
	r := bufio.NewReader(c)
	line, _ := r.ReadString('\n')
	for {
		h, err := r.ReadString('\n')
		if err != nil || h == "\r\n" {
			break
		}
	}
	return c, r, strings.TrimSpace(line)
}

// tunnelTraffic opens one working tunnel (5 bytes each way), then a refused target, a
// bad credential and a dial failure.
func tunnelTraffic(t *testing.T, addr, echoPort, deadPort string) {
	t.Helper()
	c, r, line := connect(t, addr, "localhost:"+echoPort, secretToken)
	if !strings.Contains(line, "200") {
		t.Fatalf("open: %q", line)
	}
	_, _ = c.Write([]byte("hello"))
	buf := make([]byte, 5)
	if _, err := io.ReadFull(r, buf); err != nil {
		t.Fatal(err)
	}
	_ = c.Close()
	if _, _, line := connect(t, addr, "evil.net:"+echoPort, secretToken); !strings.Contains(line, "403") {
		t.Fatalf("forbidden: %q", line)
	}
	if _, _, line := connect(t, addr, "localhost:"+echoPort, "wrong"); !strings.Contains(line, "407") {
		t.Fatalf("auth: %q", line)
	}
	if _, _, line := connect(t, addr, "localhost:"+deadPort, secretToken); !strings.Contains(line, "502") {
		t.Fatalf("dial failure: %q", line)
	}
}

func TestTunnelInfoLeaksNothing(t *testing.T) {
	addr, out, echoPort, deadPort, l := tunnelServer(t, slog.LevelInfo)
	tunnelTraffic(t, addr, echoPort, deadPort)

	got := waitLines(t, out, 1)
	if len(got) != 1 || got[0]["msg"] != "upstream error" || got[0]["mode"] != "tunnel" ||
		got[0]["reason"] != "other" || len(got[0]) != 5 {
		t.Fatalf("want the dial failure as a reason only:\n%s", out.String())
	}
	l.Flush()
	wantAbsent(t, out, "127.0.0.1", "localhost", "evil.net", secretToken, "user", echoPort, deadPort)
}

func TestTunnelDebugOneLinePerRequest(t *testing.T) {
	addr, out, echoPort, deadPort, _ := tunnelServer(t, slog.LevelDebug)
	tunnelTraffic(t, addr, echoPort, deadPort)

	got := waitLines(t, out, 5) // 4 requests + the first upstream error
	var opened map[string]any
	for _, g := range got {
		if g["msg"] == "request" && g["status"] == float64(200) {
			opened = g
		}
	}
	if opened == nil || opened["mode"] != "tunnel" || opened["method"] != "CONNECT" ||
		opened["target"] != "localhost:"+echoPort || opened["bytes"] != float64(10) {
		t.Fatalf("tunnel line (10 bytes: 5 up, 5 down):\n%s", out.String())
	}
	wantAbsent(t, out, "127.0.0.1", secretToken, "user")
}
