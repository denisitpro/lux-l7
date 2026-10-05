package relay

import (
	"crypto/tls"
	"crypto/x509"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/denisitpro/lux-l7/internal/config"
	"github.com/denisitpro/lux-l7/internal/route"
)

type sniTransport struct {
	base *http.Transport
	rt   route.Route
}

func (t *sniTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if t.rt.Upstream.Scheme != "https" {
		return t.base.RoundTrip(req)
	}
	sni := tlsServerName(t.rt, req.Host)
	if t.base.TLSClientConfig != nil && t.base.TLSClientConfig.ServerName == sni {
		return t.base.RoundTrip(req)
	}
	cloned := t.base.Clone()
	tlsCfg := cloned.TLSClientConfig
	if tlsCfg == nil {
		tlsCfg = &tls.Config{MinVersion: tls.VersionTLS12}
	} else {
		tlsCfg = tlsCfg.Clone()
	}
	tlsCfg.ServerName = sni
	cloned.TLSClientConfig = tlsCfg
	return cloned.RoundTrip(req)
}

func tlsServerName(rt route.Route, reqHost string) string {
	if s := strings.TrimSpace(rt.TLSServerName); s != "" {
		return hostOnly(s)
	}
	if s := strings.TrimSpace(rt.HostHeader); s != "" {
		return hostOnly(s)
	}
	return hostOnly(reqHost)
}

func hostOnly(authority string) string {
	h, _, err := net.SplitHostPort(authority)
	if err != nil {
		return authority
	}
	return h
}

func newTransport(cfg config.Config, rt route.Route, rootCAs *x509.CertPool) http.RoundTripper {
	tlsCfg := &tls.Config{
		MinVersion: tls.VersionTLS12,
		RootCAs:    rootCAs,
	}
	if rt.Upstream.Scheme == "https" {
		if s := strings.TrimSpace(rt.TLSServerName); s != "" {
			tlsCfg.ServerName = hostOnly(s)
		} else if s := strings.TrimSpace(rt.HostHeader); s != "" {
			tlsCfg.ServerName = hostOnly(s)
		}
	}
	base := &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: (&net.Dialer{
			Timeout:   30 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          256,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
		ResponseHeaderTimeout: cfg.UpstreamTimeout,
		TLSClientConfig:       tlsCfg,
	}
	if rt.Upstream.Scheme == "https" && rt.TLSServerName == "" && rt.HostHeader == "" {
		return &sniTransport{base: base, rt: rt}
	}
	return base
}
