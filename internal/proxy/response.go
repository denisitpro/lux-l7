package proxy

import (
	"encoding/json"
	"errors"
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
