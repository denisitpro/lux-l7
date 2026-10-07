package tunnel_test

import (
	"bufio"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/denisitpro/lux-l7/internal/auth"
	"github.com/denisitpro/lux-l7/internal/config"
	"github.com/denisitpro/lux-l7/internal/headers"
	"github.com/denisitpro/lux-l7/internal/tunnel"
)

const testToken = "s3cret"

// baseCfg allows loopback targets (guard off) on the given ports; short timeouts keep
// the suite fast.
func baseCfg(ports ...int) config.Config {
	return config.Config{
		UpstreamAllowPrivate: true,
		UpstreamTimeout:      2 * time.Second,
		ReadHeaderTimeout:    2 * time.Second,
		AllowHosts:           []string{"127.0.0.1"},
		TunnelAllowPorts:     ports,
		TunnelIdleTimeout:    5 * time.Second,
	}
}

func withAuth(cfg config.Config) config.Config {
	cfg.AuthEnabled = true
	cfg.AuthMode = config.AuthModeInstance
	cfg.InstanceToken = testToken
	return cfg
}

// start serves a tunnel on a loopback listener and returns its address.
func start(t *testing.T, cfg config.Config) (string, *tunnel.Server) {
	t.Helper()
	v, err := auth.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	srv, err := tunnel.New(cfg, v, nil)
	if err != nil {
		t.Fatal(err)
	}
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = srv.Serve(l) }()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
		defer cancel()
		_ = srv.Shutdown(ctx)
	})
	return l.Addr().String(), srv
}

// tcpServer accepts connections and runs handle on each in its own goroutine.
func tcpServer(t *testing.T, handle func(net.Conn)) (addr string, port int) {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = l.Close() })
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			go func() {
				defer func() { _ = c.Close() }()
				handle(c)
			}()
		}
	}()
	return l.Addr().String(), l.Addr().(*net.TCPAddr).Port
}

func echo(c net.Conn) { _, _ = io.Copy(c, c) }

// dial opens a client connection to the tunnel (TLS when tlsCfg is set).
func dial(t *testing.T, addr string, tlsCfg *tls.Config) net.Conn {
	t.Helper()
	var c net.Conn
	var err error
	if tlsCfg != nil {
		c, err = tls.Dial("tcp", addr, tlsCfg)
	} else {
		c, err = net.Dial("tcp", addr)
	}
	if err != nil {
		t.Fatal(err)
	}
	_ = c.SetDeadline(time.Now().Add(5 * time.Second))
	t.Cleanup(func() { _ = c.Close() })
	return c
}

func connectReq(target string, hdr map[string]string) string {
	var b strings.Builder
	b.WriteString("CONNECT " + target + " HTTP/1.1\r\nHost: " + target + "\r\n")
	for k, v := range hdr {
		b.WriteString(k + ": " + v + "\r\n")
	}
	b.WriteString("\r\n")
	return b.String()
}

func basic(user, token string) map[string]string {
	return map[string]string{"Proxy-Authorization": "Basic " + base64.StdEncoding.EncodeToString([]byte(user+":"+token))}
}

// connect sends CONNECT and returns the response plus the reader holding any bytes
// that followed it.
func connect(t *testing.T, c net.Conn, target string, hdr map[string]string) (*http.Response, *bufio.Reader) {
	t.Helper()
	if _, err := io.WriteString(c, connectReq(target, hdr)); err != nil {
		t.Fatal(err)
	}
	return readResp(t, c)
}

func readResp(t *testing.T, c net.Conn) (*http.Response, *bufio.Reader) {
	t.Helper()
	br := bufio.NewReader(c)
	res, err := http.ReadResponse(br, &http.Request{Method: http.MethodConnect})
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, res.Body)
		_ = res.Body.Close()
	}
	return res, br
}

func expectStatus(t *testing.T, addr string, target string, hdr map[string]string, want int) *http.Response {
	t.Helper()
	res, _ := connect(t, dial(t, addr, nil), target, hdr)
	if res.StatusCode != want {
		t.Fatalf("%s: status = %d, want %d", target, res.StatusCode, want)
	}
	return res
}

// selfSigned writes a self-signed pair valid for 127.0.0.1 and returns its paths.
func selfSigned(t *testing.T, dir, cn string) (certFile, keyFile string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tpl := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()),
		Subject:      pkix.Name{CommonName: cn},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
	}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	certFile, keyFile = filepath.Join(dir, "tls.crt"), filepath.Join(dir, "tls.key")
	write := func(path, typ string, b []byte) {
		if err := os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: typ, Bytes: b}), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write(certFile, "CERTIFICATE", der)
	write(keyFile, "EC PRIVATE KEY", keyDER)
	return certFile, keyFile
}

func poolFromFile(t *testing.T, certFile string) *x509.CertPool {
	t.Helper()
	pemBytes, err := os.ReadFile(certFile)
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(pemBytes) {
		t.Fatal("bad pem")
	}
	return pool
}

func TestHTTPSThroughTunnel(t *testing.T) {
	up := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("hello via tunnel"))
	}))
	t.Cleanup(up.Close)
	upPort := up.Listener.Addr().(*net.TCPAddr).Port

	for _, withTLS := range []bool{false, true} {
		name := "plain listener"
		if withTLS {
			name = "tls listener"
		}
		t.Run(name, func(t *testing.T) {
			cfg := withAuth(baseCfg(upPort))
			pool := x509.NewCertPool()
			pool.AddCert(up.Certificate())
			scheme := "http"
			if withTLS {
				certFile, keyFile := selfSigned(t, t.TempDir(), "tunnel")
				cfg.TunnelTLSCertFile, cfg.TunnelTLSKeyFile = certFile, keyFile
				pool = poolFromFile(t, certFile)
				pool.AddCert(up.Certificate())
				scheme = "https"
			}
			addr, _ := start(t, cfg)

			proxyURL := &url.URL{Scheme: scheme, Host: addr, User: url.UserPassword("user", testToken)}
			client := &http.Client{
				Timeout: 5 * time.Second,
				Transport: &http.Transport{
					Proxy:           http.ProxyURL(proxyURL),
					TLSClientConfig: &tls.Config{RootCAs: pool},
				},
			}
			res, err := client.Get(up.URL)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = res.Body.Close() }()
			body, _ := io.ReadAll(res.Body)
			if res.StatusCode != http.StatusOK || string(body) != "hello via tunnel" {
				t.Fatalf("status=%d body=%q", res.StatusCode, body)
			}
		})
	}
}

func TestTLSListenerSettings(t *testing.T) {
	certFile, keyFile := selfSigned(t, t.TempDir(), "tunnel")
	cfg := baseCfg(443)
	cfg.TunnelTLSCertFile, cfg.TunnelTLSKeyFile = certFile, keyFile
	addr, _ := start(t, cfg)

	c := tls.Client(dial(t, addr, nil), &tls.Config{RootCAs: poolFromFile(t, certFile), ServerName: "127.0.0.1", NextProtos: []string{"h2", "http/1.1"}})
	if err := c.Handshake(); err != nil {
		t.Fatal(err)
	}
	if got := c.ConnectionState().NegotiatedProtocol; got != "http/1.1" {
		t.Fatalf("ALPN = %q, want http/1.1", got)
	}

	old := &tls.Config{InsecureSkipVerify: true, MaxVersion: tls.VersionTLS11}
	if conn, err := tls.Dial("tcp", addr, old); err == nil {
		_ = conn.Close()
		t.Fatal("TLS 1.1 must be refused")
	}
}

func TestPolicyDeniesTarget(t *testing.T) {
	_, port := tcpServer(t, echo)
	cfg := baseCfg(port)
	cfg.DenyHosts = []string{"denied.example.com"}
	cfg.AllowSuffixes = []string{"example.com"}
	addr, _ := start(t, cfg)

	expectStatus(t, addr, "other.example.net:"+strconv.Itoa(port), nil, http.StatusForbidden)
	expectStatus(t, addr, "denied.example.com:"+strconv.Itoa(port), nil, http.StatusForbidden)
}

func TestPortNotAllowed(t *testing.T) {
	_, port := tcpServer(t, echo)
	addr, _ := start(t, baseCfg(port+1))
	res := expectStatus(t, addr, "127.0.0.1:"+strconv.Itoa(port), nil, http.StatusForbidden)
	if res.Header.Get("Proxy-Authenticate") != "" {
		t.Fatal("403 must not challenge")
	}
}

func TestBadTarget(t *testing.T) {
	addr, _ := start(t, baseCfg(443))
	for _, target := range []string{"127.0.0.1", "127.0.0.1:0", "127.0.0.1:99999", "127.0.0.1:abc", "u@127.0.0.1:443"} {
		expectStatus(t, addr, target, nil, http.StatusBadRequest)
	}
}

func TestAuthRequired(t *testing.T) {
	_, port := tcpServer(t, echo)
	target := "127.0.0.1:" + strconv.Itoa(port)
	addr, _ := start(t, withAuth(baseCfg(port)))

	for name, hdr := range map[string]map[string]string{
		"missing":       nil,
		"wrong token":   basic("u", "nope"),
		"empty token":   basic("u", ""),
		"not basic":     {"Proxy-Authorization": "Bearer " + testToken},
		"not base64":    {"Proxy-Authorization": "Basic !!!"},
		"no colon":      {"Proxy-Authorization": "Basic " + base64.StdEncoding.EncodeToString([]byte(testToken))},
		"token as user": basic(testToken, ""),
	} {
		t.Run(name, func(t *testing.T) {
			res := expectStatus(t, addr, target, hdr, http.StatusProxyAuthRequired)
			if got := res.Header.Get("Proxy-Authenticate"); got != `Basic realm="lux-l7"` {
				t.Fatalf("Proxy-Authenticate = %q", got)
			}
		})
	}

	// Any username works; only the token counts. Colons inside the token survive.
	expectStatus(t, addr, target, basic("whoever", testToken), http.StatusOK)
	expectStatus(t, addr, target, basic("", testToken), http.StatusOK)
}

// A refused CONNECT closes the connection instead of waiting for a next request, and
// the server's idle timeout bounds any keep-alive wait.
func TestRefusedConnectClosesConnection(t *testing.T) {
	_, port := tcpServer(t, echo)
	target := "127.0.0.1:" + strconv.Itoa(port)
	cfg := withAuth(baseCfg(port))
	cfg.IdleTimeout = 300 * time.Millisecond
	addr, _ := start(t, cfg)

	c := dial(t, addr, nil)
	res, br := connect(t, c, target, nil)
	if res.StatusCode != http.StatusProxyAuthRequired {
		t.Fatalf("status = %d, want 407", res.StatusCode)
	}
	if !res.Close {
		t.Fatal("refusal does not announce Connection: close")
	}
	_ = c.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, err := br.ReadByte(); !errors.Is(err, io.EOF) {
		t.Fatalf("read after 407 = %v, want EOF (server closed the connection)", err)
	}
}

// Credentials are checked before policy and port: a client without a valid token
// cannot tell a denied host or port from an allowed one.
func TestAuthBeforePolicyAndPort(t *testing.T) {
	_, port := tcpServer(t, echo)
	p := strconv.Itoa(port)
	cfg := withAuth(baseCfg(port + 1)) // the echo port is not allowed
	cfg.DenyHosts = []string{"denied.example.com"}
	addr, _ := start(t, cfg)

	for name, target := range map[string]string{
		"denied host":       "denied.example.com:" + p,
		"host off the list": "other.example.net:" + strconv.Itoa(port+1),
		"denied port":       "127.0.0.1:" + p,
	} {
		t.Run(name, func(t *testing.T) {
			expectStatus(t, addr, target, nil, http.StatusProxyAuthRequired)
			expectStatus(t, addr, target, basic("u", "nope"), http.StatusProxyAuthRequired)
		})
	}
	// A valid token still meets policy and port.
	expectStatus(t, addr, "denied.example.com:"+p, basic("u", testToken), http.StatusForbidden)
	expectStatus(t, addr, "127.0.0.1:"+p, basic("u", testToken), http.StatusForbidden)
}

func TestAuthMultipleInstanceTokens(t *testing.T) {
	_, port := tcpServer(t, echo)
	target := "127.0.0.1:" + strconv.Itoa(port)
	cfg := withAuth(baseCfg(port))
	cfg.InstanceToken = testToken + ", second-token ,,"
	addr, _ := start(t, cfg)

	expectStatus(t, addr, target, basic("u", testToken), http.StatusOK)
	expectStatus(t, addr, target, basic("u", "second-token"), http.StatusOK)
	expectStatus(t, addr, target, basic("u", "third-token"), http.StatusProxyAuthRequired)
}

func TestAuthPerHostToken(t *testing.T) {
	_, port := tcpServer(t, echo)
	cfg := baseCfg(port)
	cfg.AllowHosts = []string{"127.0.0.1", "localhost"}
	cfg.AuthEnabled = true
	cfg.AuthMode = config.AuthModePerHost
	cfg.HostTokenJSON = `{"127.0.0.1":"tok-a","localhost":"tok-b"}`
	addr, _ := start(t, cfg)
	p := strconv.Itoa(port)

	expectStatus(t, addr, "127.0.0.1:"+p, basic("u", "tok-a"), http.StatusOK)
	expectStatus(t, addr, "localhost:"+p, basic("u", "tok-b"), http.StatusOK)
	expectStatus(t, addr, "127.0.0.1:"+p, basic("u", "tok-b"), http.StatusProxyAuthRequired)
	expectStatus(t, addr, "localhost:"+p, basic("u", "tok-a"), http.StatusProxyAuthRequired)
}

func TestGuardBlocksLoopback(t *testing.T) {
	_, port := tcpServer(t, echo)
	cfg := baseCfg(port)
	cfg.UpstreamAllowPrivate = false
	addr, _ := start(t, cfg)
	expectStatus(t, addr, "127.0.0.1:"+strconv.Itoa(port), nil, http.StatusForbidden)
}

func TestUnreachableUpstream(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	_ = l.Close() // nothing listens here now
	addr, _ := start(t, baseCfg(port))
	expectStatus(t, addr, "127.0.0.1:"+strconv.Itoa(port), nil, http.StatusBadGateway)
}

func TestNonConnectIsRejected(t *testing.T) {
	addr, _ := start(t, baseCfg(443))
	for _, method := range []string{http.MethodGet, http.MethodPost} {
		req, _ := http.NewRequest(method, "http://"+addr+"/healthz", nil)
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(res.Body)
		_ = res.Body.Close()
		if res.StatusCode != http.StatusMethodNotAllowed || !strings.Contains(string(body), "method_not_allowed") {
			t.Fatalf("%s: status=%d body=%s", method, res.StatusCode, body)
		}
	}
}

// The client may send the TLS ClientHello in the same segment as the CONNECT; those
// bytes land in the server's read buffer and must reach the upstream.
func TestPipelinedBytesReachUpstream(t *testing.T) {
	got := make(chan string, 1)
	_, port := tcpServer(t, func(c net.Conn) {
		buf := make([]byte, len("CLIENT-HELLO"))
		_, _ = io.ReadFull(c, buf)
		got <- string(buf)
	})
	addr, _ := start(t, baseCfg(port))

	c := dial(t, addr, nil)
	target := "127.0.0.1:" + strconv.Itoa(port)
	if _, err := io.WriteString(c, connectReq(target, nil)+"CLIENT-HELLO"); err != nil {
		t.Fatal(err)
	}
	if res, _ := readResp(t, c); res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", res.StatusCode)
	}
	select {
	case s := <-got:
		if s != "CLIENT-HELLO" {
			t.Fatalf("upstream got %q", s)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("pipelined bytes never reached the upstream")
	}
}

func TestIdleTimeoutClosesIdleTunnel(t *testing.T) {
	_, port := tcpServer(t, func(c net.Conn) { _, _ = io.Copy(io.Discard, c) }) // silent
	cfg := baseCfg(port)
	cfg.TunnelIdleTimeout = 300 * time.Millisecond
	addr, _ := start(t, cfg)

	c := dial(t, addr, nil)
	res, br := connect(t, c, "127.0.0.1:"+strconv.Itoa(port), nil)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", res.StatusCode)
	}
	start := time.Now()
	if _, err := br.ReadByte(); err == nil {
		t.Fatal("expected the idle tunnel to be closed")
	}
	if d := time.Since(start); d > 3*time.Second {
		t.Fatalf("closed after %s, want about the idle timeout", d)
	}
}

func TestIdleTimeoutSparesActiveTunnel(t *testing.T) {
	cfg := func(port int) config.Config {
		c := baseCfg(port)
		c.TunnelIdleTimeout = 400 * time.Millisecond
		return c
	}

	t.Run("chatty client", func(t *testing.T) {
		_, port := tcpServer(t, echo)
		addr, _ := start(t, cfg(port))
		c := dial(t, addr, nil)
		res, br := connect(t, c, "127.0.0.1:"+strconv.Itoa(port), nil)
		if res.StatusCode != http.StatusOK {
			t.Fatalf("status = %d", res.StatusCode)
		}
		for i := 0; i < 12; i++ { // ~1.2 s, three idle periods
			if _, err := c.Write([]byte{'x'}); err != nil {
				t.Fatalf("write %d: %v", i, err)
			}
			if _, err := br.ReadByte(); err != nil {
				t.Fatalf("read %d: %v", i, err)
			}
			time.Sleep(100 * time.Millisecond)
		}
	})

	t.Run("silent client, active download", func(t *testing.T) {
		_, port := tcpServer(t, func(c net.Conn) {
			for i := 0; i < 12; i++ {
				if _, err := c.Write([]byte{'d'}); err != nil {
					return
				}
				time.Sleep(100 * time.Millisecond)
			}
		})
		addr, _ := start(t, cfg(port))
		c := dial(t, addr, nil)
		res, br := connect(t, c, "127.0.0.1:"+strconv.Itoa(port), nil)
		if res.StatusCode != http.StatusOK {
			t.Fatalf("status = %d", res.StatusCode)
		}
		if _, err := io.ReadFull(br, make([]byte, 12)); err != nil {
			t.Fatalf("download cut short: %v", err)
		}
	})
}

// After the client half-closes, the upstream can still answer.
func TestHalfClose(t *testing.T) {
	_, port := tcpServer(t, func(c net.Conn) {
		_, _ = io.Copy(io.Discard, c) // until the client's FIN
		_, _ = c.Write([]byte("bye"))
	})
	addr, _ := start(t, baseCfg(port))

	c := dial(t, addr, nil)
	res, br := connect(t, c, "127.0.0.1:"+strconv.Itoa(port), nil)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", res.StatusCode)
	}
	_, _ = c.Write([]byte("request"))
	if err := c.(*net.TCPConn).CloseWrite(); err != nil {
		t.Fatal(err)
	}
	b, err := io.ReadAll(br)
	if err != nil || string(b) != "bye" {
		t.Fatalf("got %q err=%v", b, err)
	}
}

func TestCertReload(t *testing.T) {
	dir := t.TempDir()
	certFile, keyFile := selfSigned(t, dir, "first")
	past := time.Now().Add(-time.Hour)
	for _, f := range []string{certFile, keyFile} {
		if err := os.Chtimes(f, past, past); err != nil {
			t.Fatal(err)
		}
	}
	cfg := baseCfg(443)
	cfg.TunnelTLSCertFile, cfg.TunnelTLSKeyFile = certFile, keyFile
	addr, _ := start(t, cfg)

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

	// A broken replacement keeps the old certificate in service.
	if err := os.WriteFile(certFile, []byte("garbage"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := served(); got != "first" {
		t.Fatalf("after failed reload served %q, want first", got)
	}

	// A valid pair with a newer mtime is picked up without a restart.
	selfSigned(t, dir, "second")
	if got := served(); got != "second" {
		t.Fatalf("after reload served %q, want second", got)
	}
}

func TestShutdownForceClosesOpenTunnels(t *testing.T) {
	_, port := tcpServer(t, func(c net.Conn) { _, _ = io.Copy(io.Discard, c) })
	addr, srv := start(t, baseCfg(port))

	c := dial(t, addr, nil)
	res, br := connect(t, c, "127.0.0.1:"+strconv.Itoa(port), nil)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", res.StatusCode)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	done := make(chan struct{})
	go func() {
		_ = srv.Shutdown(ctx)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("Shutdown did not return")
	}
	if _, err := br.ReadByte(); err == nil {
		t.Fatal("open tunnel must be closed after the shutdown budget")
	}
}

// openTunnel establishes a tunnel and fails the test unless it is accepted.
func openTunnel(t *testing.T, addr, target string, hdr map[string]string) net.Conn {
	t.Helper()
	c := dial(t, addr, nil)
	res, _ := connect(t, c, target, hdr)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", res.StatusCode)
	}
	return c
}

func TestCapacityRefusesBeyondLimitAndFreesSlot(t *testing.T) {
	_, port := tcpServer(t, echo)
	target := "127.0.0.1:" + strconv.Itoa(port)
	cfg := baseCfg(port)
	cfg.TunnelMaxConnections = 2
	addr, _ := start(t, cfg)

	first := openTunnel(t, addr, target, nil)
	openTunnel(t, addr, target, nil)

	res := expectStatus(t, addr, target, nil, http.StatusServiceUnavailable)
	if got := res.Header.Get("Retry-After"); got != "1" {
		t.Fatalf("Retry-After = %q", got)
	}
	if !res.Close {
		t.Fatal("capacity refusal must close the connection")
	}

	_ = first.Close()
	deadline := time.Now().Add(3 * time.Second)
	for {
		c := dial(t, addr, nil)
		res, _ := connect(t, c, target, nil)
		if res.StatusCode == http.StatusOK {
			return
		}
		if res.StatusCode != http.StatusServiceUnavailable || time.Now().After(deadline) {
			t.Fatalf("slot not freed after a tunnel closed, status = %d", res.StatusCode)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestCapacityErrorBody(t *testing.T) {
	_, port := tcpServer(t, echo)
	target := "127.0.0.1:" + strconv.Itoa(port)
	cfg := baseCfg(port)
	cfg.TunnelMaxConnections = 1
	addr, _ := start(t, cfg)
	openTunnel(t, addr, target, nil)

	c := dial(t, addr, nil)
	if _, err := io.WriteString(c, connectReq(target, nil)); err != nil {
		t.Fatal(err)
	}
	res, err := http.ReadResponse(bufio.NewReader(c), &http.Request{Method: http.MethodConnect})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(res.Body)
	if res.StatusCode != http.StatusServiceUnavailable || !strings.Contains(string(body), `"error":"tunnel_capacity"`) {
		t.Fatalf("status = %d, body = %s", res.StatusCode, body)
	}
}

func TestCapacityCheckedAfterAuth(t *testing.T) {
	_, port := tcpServer(t, echo)
	target := "127.0.0.1:" + strconv.Itoa(port)
	cfg := withAuth(baseCfg(port))
	cfg.TunnelMaxConnections = 1
	addr, _ := start(t, cfg)
	openTunnel(t, addr, target, basic("u", testToken))

	// A client without valid credentials must not learn that the proxy is full.
	expectStatus(t, addr, target, nil, http.StatusProxyAuthRequired)
	expectStatus(t, addr, target, basic("u", testToken), http.StatusServiceUnavailable)
}

func TestCapacitySlotReleasedOnDialFailure(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	dead := l.Addr().(*net.TCPAddr).Port
	_ = l.Close() // nothing listens here now
	_, live := tcpServer(t, echo)
	cfg := baseCfg(dead, live)
	cfg.TunnelMaxConnections = 1
	addr, _ := start(t, cfg)

	// A leaked slot would turn the second or third attempt into 503.
	for range 3 {
		expectStatus(t, addr, "127.0.0.1:"+strconv.Itoa(dead), nil, http.StatusBadGateway)
	}
	openTunnel(t, addr, "127.0.0.1:"+strconv.Itoa(live), nil)
}

func TestCapacityZeroIsUnlimited(t *testing.T) {
	_, port := tcpServer(t, echo)
	target := "127.0.0.1:" + strconv.Itoa(port)
	addr, _ := start(t, baseCfg(port)) // TunnelMaxConnections == 0
	for range 20 {
		openTunnel(t, addr, target, nil)
	}
}

// A tunnel refusal carries the proxy-origin marker; the 200 that opens a tunnel does not.
func TestOriginMarker(t *testing.T) {
	_, port := tcpServer(t, echo)
	target := "127.0.0.1:" + strconv.Itoa(port)
	addr, _ := start(t, baseCfg(port+1)) // port not allowed
	denied := expectStatus(t, addr, target, nil, http.StatusForbidden)
	if denied.Header.Get(headers.ProxyOrigin) != headers.ProxyOriginValue {
		t.Fatalf("refusal marker = %q", denied.Header.Get(headers.ProxyOrigin))
	}

	addr, _ = start(t, baseCfg(port))
	ok := expectStatus(t, addr, target, nil, http.StatusOK)
	if v := ok.Header.Get(headers.ProxyOrigin); v != "" {
		t.Fatalf("tunnel 200 carries marker %q", v)
	}
}
