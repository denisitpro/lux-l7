package proxy

import (
	"context"
	"crypto/tls"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"net/http/httputil"
	"strconv"
	"strings"
	"time"

	"github.com/denisitpro/lux-l7/internal/auth"
	"github.com/denisitpro/lux-l7/internal/config"
	"github.com/denisitpro/lux-l7/internal/headers"
	"github.com/denisitpro/lux-l7/internal/policy"
)

type ctxKey int

const targetKey ctxKey = 1

type target struct {
	scheme string
	host   string // authority: host or host:port
}

// Handler forwards HTTP requests to a client-selected upstream.
type Handler struct {
	cfg    config.Config
	policy *policy.Policy
	auth   *auth.Validator
	proxy  *httputil.ReverseProxy
	log    *slog.Logger
}

// New builds the L7 proxy handler.
func New(cfg config.Config, v *auth.Validator, log *slog.Logger) *Handler {
	if log == nil {
		log = slog.Default()
	}
	p := policy.New(cfg.AllowHosts, cfg.AllowSuffixes, cfg.DenyHosts)
	tr := &http.Transport{
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
		TLSClientConfig:       &tls.Config{MinVersion: tls.VersionTLS12},
	}

	h := &Handler{
		cfg:    cfg,
		policy: p,
		auth:   v,
		log:    log,
	}
	rp := &httputil.ReverseProxy{
		Transport: tr,
		Rewrite:   h.rewrite,
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			WriteUpstreamError(w, h.log, err, "path", r.URL.Path)
		},
	}
	h.proxy = rp
	return h
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	t, err := parseTarget(r, h.cfg.UpstreamScheme, h.cfg.UpstreamAllowHTTP)
	if err != nil {
		WriteJSON(w, http.StatusBadRequest, "bad_target", err.Error())
		return
	}
	if !h.policy.Allowed(t.host) {
		WriteJSON(w, http.StatusForbidden, "forbidden", "upstream host not allowed")
		return
	}
	token := r.Header.Get(headers.ProxyToken)
	if !h.auth.Authorize(token, t.host) {
		WriteJSON(w, http.StatusUnauthorized, "unauthorized", "missing or invalid proxy token")
		return
	}

	if err := LimitBody(w, r, h.cfg.UpstreamMaxBytes); err != nil {
		WriteJSON(w, http.StatusRequestEntityTooLarge, "body_too_large", err.Error())
		return
	}

	r = r.Clone(r.Context())
	StripRoutingHeaders(r.Header)

	ctx := context.WithValue(r.Context(), targetKey, t)
	h.proxy.ServeHTTP(w, r.WithContext(ctx))
}

func (h *Handler) rewrite(pr *httputil.ProxyRequest) {
	t, ok := pr.In.Context().Value(targetKey).(target)
	if !ok {
		return
	}
	pr.Out.URL.Scheme = t.scheme
	pr.Out.URL.Host = t.host
	pr.Out.Host = t.host
	StripRoutingHeaders(pr.Out.Header)
	// Hops is a route-mode loop guard; header mode never forwards a client value.
	pr.Out.Header.Del(headers.Hops)
}

func parseTarget(r *http.Request, defaultScheme string, allowHTTP bool) (target, error) {
	rawHost := strings.TrimSpace(r.Header.Get(headers.TargetHost))
	if rawHost == "" {
		return target{}, errors.New("missing X-L7-Target-Host")
	}
	if strings.ContainsAny(rawHost, "/\\?#@") {
		return target{}, errors.New("invalid X-L7-Target-Host")
	}
	rawHost = strings.ToLower(rawHost)
	if strings.HasPrefix(rawHost, "http://") || strings.HasPrefix(rawHost, "https://") {
		return target{}, errors.New("X-L7-Target-Host must not include a scheme")
	}

	scheme := strings.ToLower(strings.TrimSpace(r.Header.Get(headers.TargetScheme)))
	if scheme == "" {
		scheme = defaultScheme
	}
	if scheme != "http" && scheme != "https" {
		return target{}, errors.New("invalid X-L7-Target-Scheme")
	}
	if scheme == "http" && !allowHTTP {
		return target{}, errors.New("http upstream scheme is disabled")
	}

	host, port := rawHost, ""
	if strings.Contains(rawHost, ":") {
		var err error
		host, port, err = net.SplitHostPort(rawHost)
		if err != nil {
			return target{}, errors.New("invalid X-L7-Target-Host port")
		}
		if n, err := strconv.ParseUint(port, 10, 16); err != nil || n == 0 {
			return target{}, errors.New("invalid X-L7-Target-Host port")
		}
	}
	// One trailing dot is the same DNS name; keep it out of policy and dial.
	host = strings.TrimSuffix(host, ".")
	if host == "" {
		return target{}, errors.New("invalid X-L7-Target-Host")
	}
	if port != "" {
		host = net.JoinHostPort(host, port)
	}

	return target{scheme: scheme, host: host}, nil
}
