package front_test

import (
	"bufio"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/denisitpro/lux-l7/internal/auth"
	"github.com/denisitpro/lux-l7/internal/config"
	"github.com/denisitpro/lux-l7/internal/front"
	"github.com/denisitpro/lux-l7/internal/headers"
	"github.com/denisitpro/lux-l7/internal/proxy"
	"github.com/denisitpro/lux-l7/internal/relay"
	"github.com/denisitpro/lux-l7/internal/route"
)

// recorder is an upstream that remembers the raw request-target it was sent.
type recorder struct {
	srv  *httptest.Server
	mu   sync.Mutex
	uris []string
}

func newRecorder(t *testing.T) *recorder {
	t.Helper()
	rec := &recorder{}
	rec.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec.mu.Lock()
		rec.uris = append(rec.uris, r.RequestURI)
		rec.mu.Unlock()
		_, _ = w.Write([]byte("upstream"))
	}))
	t.Cleanup(rec.srv.Close)
	return rec
}

func (r *recorder) seen() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.uris...)
}

// rawGet writes the request line verbatim, so no client cleans the path.
func rawGet(t *testing.T, addr, target string, hdr map[string]string) *http.Response {
	t.Helper()
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	var b strings.Builder
	b.WriteString("GET " + target + " HTTP/1.1\r\n")
	for k, v := range hdr {
		b.WriteString(k + ": " + v + "\r\n")
	}
	b.WriteString("Connection: close\r\n\r\n")
	if _, err := io.WriteString(conn, b.String()); err != nil {
		t.Fatal(err)
	}
	res, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = res.Body.Close() })
	return res
}

var oddPaths = []string{"//a//b", "/a/../b", "/a/./b", "//"}

// Route mode refuses dot segments (400 bad_path, #61); the rest passes verbatim.
var routeOddPaths = []string{"//a//b", "//"}

func TestHeaderModePathsReachUpstreamUnchanged(t *testing.T) {
	rec := newRecorder(t)
	cfg := config.Config{UpstreamScheme: "http", UpstreamAllowHTTP: true, UpstreamAllowPrivate: true, UpstreamMaxBytes: 1 << 20}
	v, _ := auth.New(cfg)
	srv := httptest.NewServer(front.New(proxy.New(cfg, v, nil)))
	t.Cleanup(srv.Close)

	for _, p := range oddPaths {
		res := rawGet(t, srv.Listener.Addr().String(), p, map[string]string{
			"Host":             "proxy.example.com",
			headers.TargetHost: rec.srv.Listener.Addr().String(),
		})
		if res.StatusCode != http.StatusOK {
			t.Fatalf("%q: status=%d", p, res.StatusCode)
		}
	}
	assertSeen(t, rec.seen(), oddPaths)
}

func TestRouteModePathsReachUpstreamUnchanged(t *testing.T) {
	rec := newRecorder(t)
	routes, err := route.ParseJSON(`[{"host":"example.com","path_prefix":"/","upstream":"http://` + rec.srv.Listener.Addr().String() + `"}]`)
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{UpstreamMaxBytes: 1 << 20, RouteMaxHops: 1}
	srv := httptest.NewServer(front.New(relay.New(cfg, routes, nil, relay.Options{})))
	t.Cleanup(srv.Close)

	for _, p := range routeOddPaths {
		res := rawGet(t, srv.Listener.Addr().String(), p, map[string]string{"Host": "example.com"})
		if res.StatusCode != http.StatusOK {
			t.Fatalf("%q: status=%d", p, res.StatusCode)
		}
	}
	assertSeen(t, rec.seen(), routeOddPaths)
	for _, p := range []string{"/a/../b", "/a/./b"} {
		res := rawGet(t, srv.Listener.Addr().String(), p, map[string]string{"Host": "example.com"})
		if res.StatusCode != http.StatusBadRequest {
			t.Fatalf("%q: status=%d, want 400", p, res.StatusCode)
		}
	}
}

func assertSeen(t *testing.T, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("upstream saw %v, want %v", got, want)
	}
	for i, p := range want {
		if got[i] != p {
			t.Fatalf("upstream saw %q, want %q", got[i], p)
		}
	}
}

func TestProbesAreLocalAndGETOnly(t *testing.T) {
	var proxied []string
	app := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		proxied = append(proxied, r.Method+" "+r.URL.Path)
		w.WriteHeader(http.StatusTeapot)
	})
	srv := httptest.NewServer(front.New(app))
	t.Cleanup(srv.Close)

	for _, p := range []string{"/healthz", "/readyz", "/version"} {
		res, err := http.Get(srv.URL + p)
		if err != nil {
			t.Fatal(err)
		}
		_ = res.Body.Close()
		if res.StatusCode != http.StatusOK {
			t.Fatalf("%s: status=%d", p, res.StatusCode)
		}
	}
	if len(proxied) != 0 {
		t.Fatalf("probes reached app: %v", proxied)
	}

	res, err := http.Post(srv.URL+"/healthz", "text/plain", strings.NewReader("x"))
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != http.StatusTeapot || len(proxied) != 1 {
		t.Fatalf("POST /healthz: status=%d proxied=%v", res.StatusCode, proxied)
	}
}
