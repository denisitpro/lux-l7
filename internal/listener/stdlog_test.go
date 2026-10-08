package listener_test

import (
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/denisitpro/lux-l7/internal/config"
	"github.com/denisitpro/lux-l7/internal/discardlog/logtest"
	"github.com/denisitpro/lux-l7/internal/headers"
)

// net/http writes its own error lines (handshake errors name the client address,
// ReverseProxy body-copy errors name the upstream address) through the std logger,
// which slog.SetDefault routes into the JSON log. The listener must emit none.
func TestNoStdlibErrorLogOnTLSHandshakeFailure(t *testing.T) {
	logged := logtest.CaptureStd(t)
	cfg, _ := tlsCfg(t)
	addr := start(t, cfg)

	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	_, _ = conn.Write([]byte("\x00\x01\x02\x03\x04\x05\x06\x07\x08\x09\x0a\x0b"))
	_, _ = io.Copy(io.Discard, conn) // returns when the server drops the connection
	if got := logged(); got != "" {
		t.Fatalf("std log not empty: %q", got)
	}
}

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
	addr := start(t, config.Config{})

	req, _ := http.NewRequest(http.MethodGet, "http://"+addr+"/x", nil)
	req.Header.Set(headers.TargetHost, up.Listener.Addr().String())
	req.Header.Set(headers.TargetScheme, "http")
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
