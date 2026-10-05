package proxy

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"net/http/httputil"
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
	cfg       config.Config
	policy    *policy.Policy
	auth      *auth.Validator
	proxy     *httputil.ReverseProxy
	log       *slog.Logger
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
		MaxIdleConns:            256,
		IdleConnTimeout:         90 * time.Second,
		TLSHandshakeTimeout:     10 * time.Second,
		ExpectContinueTimeout:   1 * time.Second,
		ResponseHeaderTimeout:   cfg.UpstreamTimeout,
		TLSClientConfig:         &tls.Config{MinVersion: tls.VersionTLS12},
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
			h.log.Error("upstream", "err", err, "path", r.URL.Path)
			writeJSON(w, http.StatusBadGateway, "bad_gateway", "upstream request failed")
		},
	}
	h.proxy = rp
	return h
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	t, err := parseTarget(r, h.cfg.UpstreamScheme)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, "bad_target", err.Error())
		return
	}
	if !h.policy.Allowed(t.host) {
		writeJSON(w, http.StatusForbidden, "forbidden", "upstream host not allowed")
		return
	}
	token := r.Header.Get(headers.ProxyToken)
	if !h.auth.Authorize(token, t.host) {
		writeJSON(w, http.StatusUnauthorized, "unauthorized", "missing or invalid proxy token")
		return
	}

	if err := limitBody(w, r, h.cfg.UpstreamMaxBytes); err != nil {
		writeJSON(w, http.StatusRequestEntityTooLarge, "body_too_large", err.Error())
		return
	}

	r = r.Clone(r.Context())
	stripRoutingHeaders(r.Header)

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
	stripRoutingHeaders(pr.Out.Header)
}

func parseTarget(r *http.Request, defaultScheme string) (target, error) {
	rawHost := strings.TrimSpace(r.Header.Get(headers.TargetHost))
	if rawHost == "" {
		return target{}, errors.New("missing X-L7-Target-Host")
	}
	if strings.ContainsAny(rawHost, "/\\?#") {
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

	if _, _, err := net.SplitHostPort(rawHost); err != nil {
		if strings.Contains(rawHost, ":") {
			return target{}, errors.New("invalid X-L7-Target-Host port")
		}
	}

	return target{scheme: scheme, host: rawHost}, nil
}

func stripRoutingHeaders(h http.Header) {
	h.Del(headers.TargetHost)
	h.Del(headers.TargetScheme)
	h.Del(headers.ProxyToken)
}

func writeJSON(w http.ResponseWriter, status int, code, detail string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": code, "detail": detail})
}

func limitBody(w http.ResponseWriter, r *http.Request, max int64) error {
	if max <= 0 || r.Body == nil || r.Body == http.NoBody {
		return nil
	}
	if r.ContentLength > 0 && r.ContentLength > max {
		return errors.New("request body too large")
	}
	r.Body = http.MaxBytesReader(w, r.Body, max)
	return nil
}
