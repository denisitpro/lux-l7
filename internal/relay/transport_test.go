package relay

import (
	"net/http"
	"testing"

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
