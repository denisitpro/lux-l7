package proxy

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"

	"github.com/denisitpro/lux-l7/internal/egress"
	"github.com/denisitpro/lux-l7/internal/headers"
)

// WriteJSON writes a JSON error body in the standard proxy shape and marks the
// response as proxy-generated (headers.ProxyOrigin).
func WriteJSON(w http.ResponseWriter, status int, code, detail string) {
	w.Header().Set(headers.ProxyOrigin, headers.ProxyOriginValue)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": code, "detail": detail})
}

// LimitBody caps the request body size when max > 0.
func LimitBody(w http.ResponseWriter, r *http.Request, max int64) error {
	if max <= 0 || r.Body == nil || r.Body == http.NoBody {
		return nil
	}
	if r.ContentLength > 0 && r.ContentLength > max {
		return errors.New("request body too large")
	}
	r.Body = http.MaxBytesReader(w, r.Body, max)
	return nil
}

// StripRoutingHeaders removes client-controlled routing headers before the upstream request.
func StripRoutingHeaders(h http.Header) {
	h.Del(headers.TargetHost)
	h.Del(headers.TargetScheme)
	h.Del(headers.ProxyToken)
}

// StripOriginMarker removes a proxy-origin marker the upstream sent, so the marker on a
// response always means the proxy wrote it. Use it as ReverseProxy.ModifyResponse.
func StripOriginMarker(resp *http.Response) error {
	resp.Header.Del(headers.ProxyOrigin)
	return nil
}

// WriteUpstreamError answers a failed upstream round trip. A client cancel gets no
// response (nobody is listening); an upstream timeout is a 504; a dial the SSRF guard
// refused is a 403; a request body cut by the size limit is a 413; any other dial or
// transport failure is a 502. It logs nothing: the error text can name the dial
// address, so only the request log (internal/reqlog) reports the reason, from the
// observation (ObserveUpstreamError).
func WriteUpstreamError(w http.ResponseWriter, err error) {
	var netErr net.Error
	var tooLarge *http.MaxBytesError
	switch {
	case errors.As(err, &tooLarge):
		// A chunked body has no Content-Length for LimitBody to pre-check; the cut
		// surfaces here as a transport error.
		WriteJSON(w, http.StatusRequestEntityTooLarge, "body_too_large", "request body too large")
	case errors.Is(err, egress.ErrBlockedAddress):
		WriteJSON(w, http.StatusForbidden, "forbidden", "upstream address not allowed")
	case errors.Is(err, context.Canceled):
		// Nobody is listening.
	case errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &netErr) && netErr.Timeout()):
		WriteJSON(w, http.StatusGatewayTimeout, "upstream_timeout", "upstream request timed out")
	default:
		WriteJSON(w, http.StatusBadGateway, "bad_gateway", "upstream request failed")
	}
}
