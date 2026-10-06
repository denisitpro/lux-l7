package metrics_test

import (
	"bufio"
	"io"
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
	"github.com/denisitpro/lux-l7/internal/proxy"
	"github.com/denisitpro/lux-l7/internal/relay"
	"github.com/denisitpro/lux-l7/internal/route"
)

// The Observation wrapper sits in front of ReverseProxy: streaming and upgrades must
// still work with metrics on, and be counted as 2xx.

// frontend builds a metrics-enabled proxy in the given mode against upstream and returns
// its address, the request Host to use, the extra request headers, and the registry.
func frontend(t *testing.T, mode metrics.Mode, upstream *httptest.Server) (addr, host string, hdr map[string]string, reg *prometheus.Registry) {
	t.Helper()
	reg, m := newMetrics(metrics.Options{})
	var h http.Handler
	switch mode {
	case metrics.ModeHeader:
		cfg := config.Config{UpstreamScheme: "http", UpstreamAllowHTTP: true, UpstreamAllowPrivate: true, UpstreamTimeout: 5 * time.Second}
		v, _ := auth.New(cfg)
		h = proxy.New(cfg, v, discard, proxy.WithMetrics(m))
		host, hdr = "client.example", map[string]string{headers.TargetHost: upstream.Listener.Addr().String()}
	default:
		routes, err := route.ParseJSON(`[{"host":"example.com","path_prefix":"/","upstream":"http://` + upstream.Listener.Addr().String() + `"}]`)
		if err != nil {
			t.Fatal(err)
		}
		cfg := config.Config{UpstreamMaxBytes: 1 << 20, RouteMaxHops: 1, UpstreamTimeout: 5 * time.Second}
		h = relay.New(cfg, routes, discard, relay.Options{Metrics: m})
		host = "example.com"
	}
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return strings.TrimPrefix(srv.URL, "http://"), host, hdr, reg
}

func wait2xx(t *testing.T, reg *prometheus.Registry, mode metrics.Mode) {
	t.Helper()
	prefix := `lux_l7_requests_total{ingress_host=`
	waitFor(t, func() bool {
		for _, l := range strings.Split(scrape(t, reg), "\n") {
			if strings.HasPrefix(l, prefix) && strings.Contains(l, `mode="`+string(mode)+`"`) &&
				strings.Contains(l, `status_class="2xx"`) && strings.HasSuffix(l, " 1") {
				return true
			}
		}
		return false
	}, "a 2xx request count for "+string(mode))
}

func TestSSEStreamsWithMetrics(t *testing.T) {
	for _, mode := range []metrics.Mode{metrics.ModeHeader, metrics.ModeRoute} {
		t.Run(string(mode), func(t *testing.T) {
			up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				for i := 0; i < 5; i++ {
					_, _ = w.Write([]byte("data: tick\n\n"))
					w.(http.Flusher).Flush()
					time.Sleep(100 * time.Millisecond)
				}
			}))
			t.Cleanup(up.Close)
			addr, host, hdr, reg := frontend(t, mode, up)

			req, _ := http.NewRequest(http.MethodGet, "http://"+addr+"/", nil)
			req.Host = host
			for k, v := range hdr {
				req.Header.Set(k, v)
			}
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
			wait2xx(t, reg, mode)
		})
	}
}

func TestWebSocketUpgradeWithMetrics(t *testing.T) {
	for _, mode := range []metrics.Mode{metrics.ModeHeader, metrics.ModeRoute} {
		t.Run(string(mode), func(t *testing.T) {
			up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
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
			addr, host, hdr, reg := frontend(t, mode, up)

			conn, err := net.Dial("tcp", addr)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = conn.Close() }()
			_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
			var b strings.Builder
			b.WriteString("GET / HTTP/1.1\r\nHost: " + host + "\r\nConnection: Upgrade\r\nUpgrade: websocket\r\n")
			for k, v := range hdr {
				b.WriteString(k + ": " + v + "\r\n")
			}
			b.WriteString("\r\n")
			_, _ = conn.Write([]byte(b.String()))
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
			_ = conn.Close()
			wait2xx(t, reg, mode)
		})
	}
}
