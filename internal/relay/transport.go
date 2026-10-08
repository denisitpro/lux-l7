package relay

import (
	"crypto/tls"
	"crypto/x509"
	"net"
	"net/http"
	"time"

	"github.com/denisitpro/lux-l7/internal/config"
	"github.com/denisitpro/lux-l7/internal/egress"
	"github.com/denisitpro/lux-l7/internal/route"
)

// newTransport returns a dedicated transport for one route. SNI is fixed per
// route (rt.ServerName) and always equals the outgoing Host unless overridden,
// so a pooled connection is never reused for a different Host.
func newTransport(cfg config.Config, rt route.Route, rootCAs *x509.CertPool) *http.Transport {
	tlsCfg := &tls.Config{
		MinVersion: tls.VersionTLS12,
		RootCAs:    rootCAs,
		ServerName: rt.ServerName,
	}
	return &http.Transport{
		// No env proxy: HTTP_PROXY would bypass the per-route SNI = Host guarantee.
		Proxy: nil,
		DialContext: (&net.Dialer{
			Timeout:   cfg.UpstreamTimeout,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          256,
		MaxIdleConnsPerHost:   egress.MaxIdleConnsPerHost,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
		ResponseHeaderTimeout: cfg.UpstreamTimeout,
		TLSClientConfig:       tlsCfg,
	}
}
