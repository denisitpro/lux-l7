package proxy

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"net/http"

	"github.com/denisitpro/lux-l7/internal/headers"
)

// WriteJSON writes a JSON error body in the standard proxy shape.
func WriteJSON(w http.ResponseWriter, status int, code, detail string) {
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

// WriteUpstreamError answers a failed upstream round trip. A client cancel gets no
// response (nobody is listening) and is logged at debug; an upstream timeout is a
// 504; any other dial or transport failure is a 502. attrs are extra log fields.
func WriteUpstreamError(w http.ResponseWriter, log *slog.Logger, err error, attrs ...any) {
	var netErr net.Error
	switch {
	case errors.Is(err, context.Canceled):
		log.Debug("client canceled", append([]any{"err", err}, attrs...)...)
	case errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &netErr) && netErr.Timeout()):
		log.Warn("upstream timeout", append([]any{"err", err}, attrs...)...)
		WriteJSON(w, http.StatusGatewayTimeout, "upstream_timeout", "upstream request timed out")
	default:
		log.Error("upstream", append([]any{"err", err}, attrs...)...)
		WriteJSON(w, http.StatusBadGateway, "bad_gateway", "upstream request failed")
	}
}
