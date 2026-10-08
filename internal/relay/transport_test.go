package relay

import (
	"net/http"
	"testing"
	"time"

	"github.com/denisitpro/lux-l7/internal/config"
	"github.com/denisitpro/lux-l7/internal/route"
)

// The route transport must never pick up HTTP_PROXY / HTTPS_PROXY: a hop through an
// env proxy would break the per-route SNI = Host guarantee. net/http reads the proxy
// environment once per process and skips loopback targets, so a black-box request
// cannot prove this; the transport's Proxy func is checked directly instead.
func TestTransportIgnoresEnvProxy(t *testing.T) {
	t.Setenv("HTTP_PROXY", "http://127.0.0.1:1")
	t.Setenv("HTTPS_PROXY", "http://127.0.0.1:1")
	t.Setenv("NO_PROXY", "")

	tr := newTransport(config.Config{}, route.Route{}, nil)
	if tr.Proxy != nil {
		req, _ := http.NewRequest(http.MethodGet, "https://origin.example.com/", nil)
		u, err := tr.Proxy(req)
		t.Fatalf("route transport has a Proxy func (returns %v, %v); it must be nil", u, err)
	}
}

// The idle pool is sized per upstream host (net/http defaults to 2), and the
// response-header wait follows UPSTREAM_TIMEOUT.
func TestTransportPoolAndTimeout(t *testing.T) {
	tr := newTransport(config.Config{UpstreamTimeout: 7 * time.Second}, route.Route{}, nil)
	if tr.MaxIdleConnsPerHost != 64 {
		t.Fatalf("MaxIdleConnsPerHost = %d, want 64", tr.MaxIdleConnsPerHost)
	}
	if tr.ResponseHeaderTimeout != 7*time.Second {
		t.Fatalf("ResponseHeaderTimeout = %v, want 7s", tr.ResponseHeaderTimeout)
	}
}
