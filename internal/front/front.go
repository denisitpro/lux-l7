// Package front serves the local probe endpoints and hands every other request,
// verbatim, to the proxy handler.
package front

import (
	"net/http"

	"github.com/denisitpro/lux-l7/internal/version"
)

// New answers GET /healthz, /readyz and /version locally. Unlike http.ServeMux it
// never cleans the path or redirects, so //a//b and /a/../b reach app unchanged.
func New(app http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			switch r.URL.Path {
			case "/healthz", "/readyz":
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte("ok"))
				return
			case "/version":
				version.Handler(w, r)
				return
			}
		}
		app.ServeHTTP(w, r)
	})
}
