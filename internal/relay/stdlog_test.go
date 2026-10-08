package relay_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/denisitpro/lux-l7/internal/discardlog/logtest"
	"github.com/denisitpro/lux-l7/internal/relay"
)

// httputil.ReverseProxy logs a body-copy failure through the std logger, with the
// upstream address in the text; the route proxy must stay silent.
func TestNoStdlibErrorLogOnBodyCopyFailure(t *testing.T) {
	logged := logtest.CaptureStd(t)
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "100")
		_, _ = w.Write([]byte("partial"))
		w.(http.Flusher).Flush()
		conn, _, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Error(err)
			return
		}
		_ = conn.Close() // upstream drops the connection mid-body
	}))
	t.Cleanup(up.Close)
	routes := mustRoutes(t, `[{"host":"example.com","path_prefix":"/","upstream":"`+upstreamURL(up)+`"}]`)
	srv := httptest.NewServer(relay.New(testCfg(), routes, nil, relay.Options{}))
	t.Cleanup(srv.Close)

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/x", nil)
	req.Host = "example.com"
	// The proxy aborts the client connection: Do or the body read fails, either way
	// only after the proxy handler has hit (and not logged) the copy error.
	if res, err := http.DefaultClient.Do(req); err == nil {
		_, _ = io.Copy(io.Discard, res.Body)
		_ = res.Body.Close()
	}
	if got := logged(); got != "" {
		t.Fatalf("std log not empty: %q", got)
	}
}
