// Package version holds release identity injected at link time (default dev).
package version

import (
	"encoding/json"
	"net/http"
)

// Build identifies the running binary for debug (tag_or_branch + short sha, or dev).
var Build = "dev"

// Handler serves the build identity as JSON; it needs no auth and is never proxied.
func Handler(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(struct {
		Version string `json:"version"`
	}{Version: Build})
}
