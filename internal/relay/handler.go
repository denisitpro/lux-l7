// Package relay implements static route-table reverse proxy mode.
package relay

import (
	"crypto/x509"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strconv"
	"strings"

	"github.com/denisitpro/lux-l7/internal/config"
	"github.com/denisitpro/lux-l7/internal/headers"
	"github.com/denisitpro/lux-l7/internal/metrics"
	"github.com/denisitpro/lux-l7/internal/proxy"
	"github.com/denisitpro/lux-l7/internal/route"
)

const statusLoopDetected = 508

// Options configures relay construction (tests may inject RootCAs).
type Options struct {
	RootCAs *x509.CertPool
	// Observer receives every request outcome (metrics, request log); nil records nothing.
	Observer *metrics.Observer
}

// Handler forwards requests using a static route table.
type Handler struct {
	cfg      config.Config
	routes   []route.Route
	proxies  []*httputil.ReverseProxy
	maxHops  int
	log      *slog.Logger
	observer *metrics.Observer
}

// New builds the route-mode handler.
func New(cfg config.Config, routes []route.Route, log *slog.Logger, opts Options) *Handler {
	if log == nil {
		log = slog.Default()
	}
	h := &Handler{
		cfg:      cfg,
		routes:   routes,
		maxHops:  cfg.RouteMaxHops,
		log:      log,
		observer: opts.Observer,
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
			ModifyResponse: proxy.StripOriginMarker,
			ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
				// Logging is the request log's job (internal/reqlog), from the observation.
				proxy.ObserveUpstreamError(metrics.From(w), err)
				proxy.WriteUpstreamError(w, err)
			},
		}
	}
	return h
}

func (h *Handler) ServeHTTP(rw http.ResponseWriter, r *http.Request) {
	w, obs := h.observer.Begin(metrics.ModeRoute, rw, r)
	defer obs.Done()
	if r.Method == http.MethodConnect {
		// CONNECT tunnels are a separate mode (#23); never relay them as plain requests.
		obs.Reject(metrics.RejectMethodNotAllowed)
		proxy.WriteJSON(w, http.StatusMethodNotAllowed, "method_not_allowed", "CONNECT is not supported in route mode")
		return
	}
	idx := route.Match(h.routes, r.Host, r.URL.Path)
	if idx < 0 {
		obs.Reject(metrics.RejectNoRoute)
		proxy.WriteJSON(w, http.StatusNotFound, "no_route", "no matching route")
		return
	}

	obs.Route(idx, h.routes[idx].Host, h.routes[idx].Upstream.Hostname())

	inHops, err := parseHops(r.Header.Get(headers.Hops))
	if err != nil {
		obs.Reject(metrics.RejectBadHops)
		proxy.WriteJSON(w, http.StatusBadRequest, "bad_hops", err.Error())
		return
	}
	if inHops >= h.maxHops {
		obs.Reject(metrics.RejectLoopDetected)
		proxy.WriteJSON(w, statusLoopDetected, "loop_detected", "hop limit exceeded")
		return
	}

	// rw, not w: MaxBytesReader needs the server's own writer to close the connection.
	if err := proxy.LimitBody(rw, r, h.cfg.UpstreamMaxBytes); err != nil {
		obs.Reject(metrics.RejectBodyTooLarge)
		proxy.WriteJSON(w, http.StatusRequestEntityTooLarge, "body_too_large", err.Error())
		return
	}

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

	outPath, outRaw := pr.In.URL.Path, pr.In.URL.RawPath
	if rt.RewritePrefix != "" {
		outPath, outRaw = rewriteEscaped(pr.In.URL, rt)
	}
	pr.Out.URL.Scheme = rt.Upstream.Scheme
	pr.Out.URL.Host = rt.Upstream.Host
	pr.Out.URL.Path = outPath
	pr.Out.URL.RawPath = outRaw
	pr.Out.URL.RawQuery = pr.In.URL.RawQuery

	pr.Out.Host = rt.OutHost

	proxy.StripRoutingHeaders(pr.Out.Header)
	// Set here, on the outgoing request: ReverseProxy removes headers named in the
	// client's Connection header before Rewrite, so a counter set earlier could be
	// stripped by the client. The inbound value was validated in ServeHTTP.
	inHops, _ := parseHops(pr.In.Header.Get(headers.Hops))
	pr.Out.Header.Set(headers.Hops, strconv.Itoa(inHops+1))

	// Client IP forwarding (X-Forwarded-For); trusted-hop handling is #8.
	pr.SetXForwarded()
	pr.Out.Header.Set("X-Forwarded-Host", pr.In.Host)
	// The proxy's own view of the client connection, never a client-sent value.
	proto := "http"
	if pr.In.TLS != nil {
		proto = "https"
	}
	pr.Out.Header.Set("X-Forwarded-Proto", proto)
}

// rewriteEscaped applies rewrite_prefix on the escaped path so encoded
// characters in the remainder (e.g. %2F) survive. It falls back to the decoded
// path when the escaped form cannot be rewritten consistently.
func rewriteEscaped(u *url.URL, rt route.Route) (path, raw string) {
	escaped := u.EscapedPath()
	if route.PathMatches(rt.PathPrefix, escaped) {
		rewritten := route.RewritePath(escaped, rt.PathPrefix, rt.RewritePrefix)
		if decoded, err := url.PathUnescape(rewritten); err == nil {
			return decoded, rewritten
		}
	}
	return route.RewritePath(u.Path, rt.PathPrefix, rt.RewritePrefix), ""
}
