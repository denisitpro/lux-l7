// Package relay implements static route-table reverse proxy mode.
package relay

import (
	"crypto/x509"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httputil"
	"strconv"
	"strings"

	"github.com/denisitpro/lux-l7/internal/config"
	"github.com/denisitpro/lux-l7/internal/headers"
	"github.com/denisitpro/lux-l7/internal/proxy"
	"github.com/denisitpro/lux-l7/internal/route"
)

const statusLoopDetected = 508

// Options configures relay construction (tests may inject RootCAs).
type Options struct {
	RootCAs *x509.CertPool
}

// Handler forwards requests using a static route table.
type Handler struct {
	cfg     config.Config
	routes  []route.Route
	proxies []*httputil.ReverseProxy
	maxHops int
	log     *slog.Logger
}

// New builds the route-mode handler.
func New(cfg config.Config, routes []route.Route, log *slog.Logger, opts Options) *Handler {
	if log == nil {
		log = slog.Default()
	}
	h := &Handler{
		cfg:     cfg,
		routes:  routes,
		maxHops: cfg.RouteMaxHops,
		log:     log,
	}
	h.proxies = make([]*httputil.ReverseProxy, len(routes))
	for i, rt := range routes {
		tr := newTransport(cfg, rt, opts.RootCAs) // one transport per route
		idx := i
		h.proxies[i] = &httputil.ReverseProxy{
			Transport: tr,
			Rewrite: func(pr *httputil.ProxyRequest) {
				h.rewrite(pr, idx)
			},
			ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
				h.log.Error("upstream", "err", err, "route", idx, "upstream", rt.Upstream.String())
				proxy.WriteJSON(w, http.StatusBadGateway, "bad_gateway", "upstream request failed")
			},
		}
	}
	return h
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	idx := route.Match(h.routes, r.Host, r.URL.Path)
	if idx < 0 {
		proxy.WriteJSON(w, http.StatusNotFound, "no_route", "no matching route")
		return
	}

	inHops, err := parseHops(r.Header.Get(headers.Hops))
	if err != nil {
		proxy.WriteJSON(w, http.StatusBadRequest, "bad_hops", err.Error())
		return
	}
	if inHops >= h.maxHops {
		proxy.WriteJSON(w, statusLoopDetected, "loop_detected", "hop limit exceeded")
		return
	}

	if err := proxy.LimitBody(w, r, h.cfg.UpstreamMaxBytes); err != nil {
		proxy.WriteJSON(w, http.StatusRequestEntityTooLarge, "body_too_large", err.Error())
		return
	}

	r = r.Clone(r.Context())
	r.Header.Set(headers.Hops, strconv.Itoa(inHops+1))
	h.proxies[idx].ServeHTTP(w, r)
}

func parseHops(raw string) (int, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 0 {
		return 0, fmt.Errorf("invalid X-L7-Hops")
	}
	return n, nil
}

func (h *Handler) rewrite(pr *httputil.ProxyRequest, idx int) {
	rt := h.routes[idx]

	outPath := pr.In.URL.Path
	if rt.RewritePrefix != "" {
		outPath = route.RewritePath(outPath, rt.PathPrefix, rt.RewritePrefix)
	}
	pr.Out.URL.Scheme = rt.Upstream.Scheme
	pr.Out.URL.Host = rt.Upstream.Host
	pr.Out.URL.Path = outPath
	pr.Out.URL.RawPath = ""
	pr.Out.URL.RawQuery = pr.In.URL.RawQuery

	if rt.HostHeader != "" {
		pr.Out.Host = rt.HostHeader
	} else {
		pr.Out.Host = pr.In.Host
	}

	proxy.StripRoutingHeaders(pr.Out.Header)

	// Client IP forwarding (X-Forwarded-For); trusted-hop handling is #8.
	pr.SetXForwarded()
	pr.Out.Header.Set("X-Forwarded-Host", pr.In.Host)
	proto := pr.In.Header.Get("X-Forwarded-Proto")
	if proto == "" {
		if pr.In.TLS != nil {
			proto = "https"
		} else {
			proto = "http"
		}
	}
	pr.Out.Header.Set("X-Forwarded-Proto", proto)
}
