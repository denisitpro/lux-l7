package listener_test

import (
	"bufio"
	"context"
	"crypto/tls"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/denisitpro/lux-l7/internal/auth"
	"github.com/denisitpro/lux-l7/internal/certs/certstest"
	"github.com/denisitpro/lux-l7/internal/config"
	"github.com/denisitpro/lux-l7/internal/front"
	"github.com/denisitpro/lux-l7/internal/headers"
	"github.com/denisitpro/lux-l7/internal/listener"
	"github.com/denisitpro/lux-l7/internal/proxy"
)

func quiet() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// upstream is a plain HTTP/1.1 server with a ping endpoint and a raw WebSocket-style echo.
func upstream(t *testing.T) string {
	t.Helper()
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Upgrade") == "websocket" {
			conn, rw, err := w.(http.Hijacker).Hijack()
			if err != nil {
				t.Error(err)
				return
			}
			defer func() { _ = conn.Close() }()
			_, _ = rw.WriteString("HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n\r\n")
			_ = rw.Flush()
			buf := make([]byte, 4)
			if _, err := io.ReadFull(rw, buf); err == nil {
				_, _ = conn.Write(append([]byte("echo:"), buf...))
			}
			return
		}
		_, _ = w.Write([]byte("pong"))
	}))
	t.Cleanup(up.Close)
	return up.Listener.Addr().String()
}

// start serves a header-mode proxy (behind the probe front) on a fresh listener.
func start(t *testing.T, cfg config.Config) string {
	t.Helper()
	cfg.UpstreamScheme = "http"
	cfg.UpstreamAllowHTTP = true
	cfg.UpstreamAllowPrivate = true
	cfg.AllowHosts = []string{"127.0.0.1"}
	v, err := auth.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	srv, err := listener.New(cfg, front.New(proxy.New(cfg, v, quiet())), quiet())
	if err != nil {
		t.Fatal(err)
	}
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = srv.Serve(l)
	}()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
		<-done
	})
	return l.Addr().String()
}

func tlsCfg(t *testing.T) (config.Config, string) {
	t.Helper()
	certFile, keyFile := certstest.SelfSigned(t, t.TempDir(), "main")
	return config.Config{HTTPTLSCertFile: certFile, HTTPTLSKeyFile: keyFile}, certFile
}

func get(t *testing.T, c *http.Client, url, target string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set(headers.TargetHost, target)
	req.Header.Set(headers.TargetScheme, "http")
	res, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = res.Body.Close() })
	return res
}

func TestTLSServesHTTP2EndToEnd(t *testing.T) {
	cfg, certFile := tlsCfg(t)
	addr := start(t, cfg)
	target := upstream(t)

	c := &http.Client{Transport: &http.Transport{
		ForceAttemptHTTP2: true,
		TLSClientConfig:   &tls.Config{RootCAs: certstest.Pool(t, certFile)},
	}}
	res := get(t, c, "https://"+addr+"/x", target)
	body, _ := io.ReadAll(res.Body)
	if res.ProtoMajor != 2 || res.StatusCode != http.StatusOK || string(body) != "pong" {
		t.Fatalf("proto=%d status=%d body=%q", res.ProtoMajor, res.StatusCode, body)
	}

	// Probes are served locally over the same listener.
	probe, err := c.Get("https://" + addr + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = probe.Body.Close() }()
	if probe.StatusCode != http.StatusOK || probe.ProtoMajor != 2 {
		t.Fatalf("probe status=%d proto=%d", probe.StatusCode, probe.ProtoMajor)
	}
}

func TestTLSFallsBackToHTTP11(t *testing.T) {
	cfg, certFile := tlsCfg(t)
	addr := start(t, cfg)
	target := upstream(t)

	c := &http.Client{Transport: &http.Transport{
		TLSClientConfig: &tls.Config{RootCAs: certstest.Pool(t, certFile), NextProtos: []string{"http/1.1"}},
	}}
	res := get(t, c, "https://"+addr+"/x", target)
	body, _ := io.ReadAll(res.Body)
	if res.ProtoMajor != 1 || string(body) != "pong" {
		t.Fatalf("proto=%d body=%q", res.ProtoMajor, body)
	}
}

func TestTLSMinimumVersion(t *testing.T) {
	cfg, certFile := tlsCfg(t)
	addr := start(t, cfg)
	_, err := tls.Dial("tcp", addr, &tls.Config{
		RootCAs:    certstest.Pool(t, certFile),
		MaxVersion: tls.VersionTLS11,
	})
	if err == nil {
		t.Fatal("TLS 1.1 handshake must be refused")
	}
}

// WebSocket clients speak HTTP/1.1 over the TLS listener: net/http's server has no
// RFC 8441 extended CONNECT, so ALPN must carry http/1.1 for the upgrade to work.
func TestWebSocketUpgradeOverTLSHTTP11(t *testing.T) {
	cfg, certFile := tlsCfg(t)
	addr := start(t, cfg)
	target := upstream(t)

	conn, err := tls.Dial("tcp", addr, &tls.Config{
		RootCAs:    certstest.Pool(t, certFile),
		NextProtos: []string{"http/1.1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	_, _ = conn.Write([]byte("GET / HTTP/1.1\r\nHost: client.example\r\nConnection: Upgrade\r\nUpgrade: websocket\r\n" +
		headers.TargetHost + ": " + target + "\r\n" + headers.TargetScheme + ": http\r\n\r\n"))
	br := bufio.NewReader(conn)
	res, err := http.ReadResponse(br, nil)
	if err != nil || res.StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("handshake: res=%v err=%v", res, err)
	}
	_, _ = conn.Write([]byte("ping"))
	got := make([]byte, len("echo:ping"))
	if _, err := io.ReadFull(br, got); err != nil || string(got) != "echo:ping" {
		t.Fatalf("echo %q err=%v", got, err)
	}
}

func TestCertReloadOnMainListener(t *testing.T) {
	dir := t.TempDir()
	certFile, keyFile := certstest.SelfSigned(t, dir, "first")
	past := time.Now().Add(-time.Hour)
	for _, f := range []string{certFile, keyFile} {
		if err := os.Chtimes(f, past, past); err != nil {
			t.Fatal(err)
		}
	}
	addr := start(t, config.Config{HTTPTLSCertFile: certFile, HTTPTLSKeyFile: keyFile})
	served := func() string {
		c, err := tls.Dial("tcp", addr, &tls.Config{InsecureSkipVerify: true})
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = c.Close() }()
		return c.ConnectionState().PeerCertificates[0].Subject.CommonName
	}
	if got := served(); got != "first" {
		t.Fatalf("served %q, want first", got)
	}
	certstest.SelfSigned(t, dir, "second")
	// The reloader checks mtimes at most once per second; wait out the interval.
	time.Sleep(1100 * time.Millisecond)
	if got := served(); got != "second" {
		t.Fatalf("after rotation served %q, want second", got)
	}
}

func TestNewFailsOnBadPair(t *testing.T) {
	certFile, _ := certstest.SelfSigned(t, t.TempDir(), "a")
	_, otherKey := certstest.SelfSigned(t, t.TempDir(), "b")
	for name, cfg := range map[string]config.Config{
		"mismatched": {HTTPTLSCertFile: certFile, HTTPTLSKeyFile: otherKey},
		"missing":    {HTTPTLSCertFile: certFile + ".missing", HTTPTLSKeyFile: otherKey},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := listener.New(cfg, http.NotFoundHandler(), quiet()); err == nil {
				t.Fatal("expected startup error")
			}
		})
	}
}

// h2cClient sends HTTP/2 over cleartext TCP with prior knowledge and nothing else.
func h2cClient() *http.Client {
	var p http.Protocols
	p.SetUnencryptedHTTP2(true)
	return &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{Protocols: &p}}
}

func TestH2CPriorKnowledgeWhenEnabled(t *testing.T) {
	addr := start(t, config.Config{HTTPH2C: true})
	target := upstream(t)

	res := get(t, h2cClient(), "http://"+addr+"/x", target)
	body, _ := io.ReadAll(res.Body)
	if res.ProtoMajor != 2 || string(body) != "pong" {
		t.Fatalf("proto=%d body=%q", res.ProtoMajor, body)
	}

	// HTTP/1.1 still works on the same port.
	res1 := get(t, http.DefaultClient, "http://"+addr+"/x", target)
	body1, _ := io.ReadAll(res1.Body)
	if res1.ProtoMajor != 1 || string(body1) != "pong" {
		t.Fatalf("http/1.1 proto=%d body=%q", res1.ProtoMajor, body1)
	}
}

func TestH2CRejectedWhenDisabled(t *testing.T) {
	addr := start(t, config.Config{})
	target := upstream(t)

	req, _ := http.NewRequest(http.MethodGet, "http://"+addr+"/x", nil)
	req.Header.Set(headers.TargetHost, target)
	if res, err := h2cClient().Do(req); err == nil {
		_ = res.Body.Close()
		t.Fatalf("h2c prior knowledge must fail without HTTP_H2C, got proto %d status %d", res.ProtoMajor, res.StatusCode)
	}

	// Plain HTTP/1.1 is unchanged.
	res := get(t, http.DefaultClient, "http://"+addr+"/x", target)
	body, _ := io.ReadAll(res.Body)
	if res.ProtoMajor != 1 || !strings.Contains(string(body), "pong") {
		t.Fatalf("proto=%d body=%q", res.ProtoMajor, body)
	}
}

func TestH2CFlagIsIgnoredWithTLS(t *testing.T) {
	cfg, certFile := tlsCfg(t)
	cfg.HTTPH2C = true
	addr := start(t, cfg)
	target := upstream(t)

	if res, err := h2cClient().Get("http://" + addr + "/healthz"); err == nil {
		_ = res.Body.Close()
		t.Fatal("cleartext request must not succeed on a TLS listener")
	}
	c := &http.Client{Transport: &http.Transport{
		ForceAttemptHTTP2: true,
		TLSClientConfig:   &tls.Config{RootCAs: certstest.Pool(t, certFile)},
	}}
	if res := get(t, c, "https://"+addr+"/x", target); res.ProtoMajor != 2 {
		t.Fatalf("proto=%d", res.ProtoMajor)
	}
}
