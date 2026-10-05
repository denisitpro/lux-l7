package version_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/denisitpro/lux-l7/internal/version"
)

func TestHandler(t *testing.T) {
	old := version.Build
	version.Build = "0.2.1-abc123def456"
	t.Cleanup(func() { version.Build = old })

	rec := httptest.NewRecorder()
	version.Handler(rec, httptest.NewRequest(http.MethodGet, "/version", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("content type %q", ct)
	}
	var body map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body) != 1 || body["version"] != "0.2.1-abc123def456" {
		t.Fatalf("body %q", rec.Body.String())
	}
}
