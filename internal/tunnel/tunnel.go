// Package tunnel serves HTTP CONNECT on its own listener: the client names the target
// in the request authority, the proxy checks policy, port and credentials, dials it
// through the SSRF-guarded dialer and then relays raw bytes. TLS stays end to end
// between the client and the target; the proxy never sees plaintext.
package tunnel

import (
	"context"
	"crypto/tls"
	"encoding/base64"
	"errors"
	"io"
	stdlog "log"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/denisitpro/lux-l7/internal/auth"
	"github.com/denisitpro/lux-l7/internal/config"
	"github.com/denisitpro/lux-l7/internal/egress"
	"github.com/denisitpro/lux-l7/internal/metrics"
	"github.com/denisitpro/lux-l7/internal/policy"
	"github.com/denisitpro/lux-l7/internal/proxy"
)

const (
	established  = "HTTP/1.1 200 Connection Established\r\n\r\n"
	proxyRealm   = `Basic realm="lux-l7"`
	copyBufBytes = 32 << 10
)

// Server is the CONNECT listener. Hijacked tunnels are tracked so Shutdown can wait
// for them and then force-close the rest.
type Server struct {
	policy *policy.Policy
	auth   *auth.Validator
	dialer *net.Dialer
	ports  map[int]struct{}
	idle   time.Duration
	// slots is a semaphore of free tunnel slots; nil means unlimited.
	slots chan struct{}
	log   *slog.Logger

	observer *metrics.Observer // nil: records nothing

	srv    *http.Server
	tlsCfg *tls.Config // nil: plain listener

	mu      sync.Mutex
	closing bool
	conns   map[net.Conn]struct{}
	wg      sync.WaitGroup
}

// Option customizes New.
type Option func(*Server)

// WithObserver makes the server report every tunnel to o (metrics, request log).
func WithObserver(o *metrics.Observer) Option {
	return func(s *Server) { s.observer = o }
}

// New builds the tunnel server. It loads the TLS pair when configured, so a bad
// certificate fails startup.
func New(cfg config.Config, v *auth.Validator, log *slog.Logger, opts ...Option) (*Server, error) {
	if log == nil {
		log = slog.Default()
	}
	s := &Server{
		policy: policy.New(cfg.AllowHosts, cfg.AllowSuffixes, cfg.DenyHosts),
		auth:   v,
		dialer: egress.NewDialer(cfg.UpstreamAllowPrivate, cfg.UpstreamTimeout),
		ports:  make(map[int]struct{}, len(cfg.TunnelAllowPorts)),
		idle:   cfg.TunnelIdleTimeout,
		log:    log,
		conns:  make(map[net.Conn]struct{}),
	}
	if cfg.TunnelMaxConnections < 0 {
		return nil, errors.New("tunnel max connections must be >= 0")
	}
	if cfg.TunnelMaxConnections > 0 {
		s.slots = make(chan struct{}, cfg.TunnelMaxConnections)
	}
	for _, o := range opts {
		o(s)
	}
	for _, p := range cfg.TunnelAllowPorts {
		s.ports[p] = struct{}{}
	}
	if s.idle <= 0 {
		return nil, errors.New("tunnel idle timeout must be > 0")
	}

	switch {
	case cfg.TunnelTLSCertFile != "" && cfg.TunnelTLSKeyFile != "":
		r, err := newCertReloader(cfg.TunnelTLSCertFile, cfg.TunnelTLSKeyFile, log)
		if err != nil {
			return nil, err
		}
		s.tlsCfg = &tls.Config{
			MinVersion: tls.VersionTLS12,
			// http/1.1 only: an h2 CONNECT stream cannot be hijacked.
			NextProtos:     []string{"http/1.1"},
			GetCertificate: r.GetCertificate,
		}
	case cfg.TunnelTLSCertFile != "" || cfg.TunnelTLSKeyFile != "":
		return nil, errors.New("TUNNEL_TLS_CERT_FILE and TUNNEL_TLS_KEY_FILE must be set together")
	default:
		log.Warn("tunnel listener has no TLS: client credentials travel in clear text, local tests only")
	}

	s.srv = &http.Server{
		Addr:    cfg.TunnelAddr,
		Handler: s,
		// No Read/Write timeouts: they would cut an established tunnel. Idle time is
		// enforced per tunnel by the relay.
		ReadHeaderTimeout: cfg.ReadHeaderTimeout,
		// Backstop for keep-alive connections waiting for a next request.
		IdleTimeout: cfg.IdleTimeout,
		// Empty, non-nil: never negotiate h2 on the TLS listener.
		TLSNextProto: map[string]func(*http.Server, *tls.Conn, http.Handler){},
		// The default error log prints the client address on handshake failures.
		ErrorLog: stdlog.New(io.Discard, "", 0),
	}
	return s, nil
}

// ListenAndServe listens on the configured address and serves until Shutdown.
func (s *Server) ListenAndServe() error {
	l, err := net.Listen("tcp", s.srv.Addr)
	if err != nil {
		return err
	}
	return s.Serve(l)
}

// Serve serves on l (wrapped in TLS when a certificate is configured).
func (s *Server) Serve(l net.Listener) error {
	if s.tlsCfg != nil {
		l = tls.NewListener(l, s.tlsCfg)
	}
	return s.srv.Serve(l)
}

// Shutdown stops accepting, waits for open tunnels until ctx ends, then force-closes
// whatever is left.
func (s *Server) Shutdown(ctx context.Context) error {
	s.mu.Lock()
	s.closing = true
	s.mu.Unlock()

	err := s.srv.Shutdown(ctx)

	done := make(chan struct{})
	go func() {
		s.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-ctx.Done():
		s.mu.Lock()
		n := len(s.conns) / 2
		for c := range s.conns {
			_ = c.Close()
		}
		s.mu.Unlock()
		s.log.Info("tunnel shutdown: force-closed open tunnels", "count", n)
		<-done
	}
	return err
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// The client names only the target, so there is no ingress host: the label stays "other".
	w, obs := s.observer.Begin(metrics.ModeTunnel, w, r)
	defer obs.Done()
	// Every response written through w is a refusal or an error (a tunnel is hijacked
	// and answered raw), so never keep the connection for another request.
	w.Header().Set("Connection", "close")
	if r.Method != http.MethodConnect {
		obs.Reject(metrics.RejectMethodNotAllowed)
		w.Header().Set("Allow", http.MethodConnect)
		proxy.WriteJSON(w, http.StatusMethodNotAllowed, "method_not_allowed", "only CONNECT is supported on the tunnel listener")
		return
	}
	host, port, err := policy.SplitAuthority(r.Host)
	if err != nil || port == "" {
		obs.Reject(metrics.RejectBadTarget)
		proxy.WriteJSON(w, http.StatusBadRequest, "bad_target", "CONNECT target must be host:port")
		return
	}
	authority := net.JoinHostPort(host, port)
	obs.Upstream(s.policy, authority)
	// Credentials first: an unauthenticated client must not learn the allow list or
	// port list from 403 versus 407.
	if !s.auth.Authorize(proxyToken(r.Header), authority) {
		obs.Reject(metrics.RejectProxyAuth)
		w.Header().Set("Proxy-Authenticate", proxyRealm)
		proxy.WriteJSON(w, http.StatusProxyAuthRequired, "proxy_auth_required", "missing or invalid proxy credentials")
		return
	}
	if !s.policy.Allowed(authority) {
		obs.Reject(metrics.RejectForbiddenHost)
		proxy.WriteJSON(w, http.StatusForbidden, "forbidden", "upstream host not allowed")
		return
	}
	if n, _ := strconv.Atoi(port); !s.portAllowed(n) { // SplitAuthority validated the port
		obs.Reject(metrics.RejectForbiddenPort)
		proxy.WriteJSON(w, http.StatusForbidden, "forbidden", "upstream port not allowed")
		return
	}
	if s.isClosing() {
		obs.Reject(metrics.RejectShuttingDown)
		proxy.WriteJSON(w, http.StatusServiceUnavailable, "shutting_down", "proxy is shutting down")
		return
	}
	// The slot is taken before dialing and given back when the handler returns, so
	// dial failures, hijack errors and a finished tunnel all release it exactly once.
	if !s.acquire() {
		obs.Reject(metrics.RejectTunnelCapacity)
		w.Header().Set("Retry-After", "1")
		proxy.WriteJSON(w, http.StatusServiceUnavailable, "tunnel_capacity", "tunnel capacity reached, retry shortly")
		return
	}
	defer s.release()

	up, err := s.dialer.DialContext(r.Context(), "tcp", authority)
	if err != nil {
		proxy.ObserveUpstreamError(obs, err)
		proxy.WriteUpstreamError(w, err)
		return
	}
	client, bufrw, err := http.NewResponseController(w).Hijack()
	if err != nil {
		_ = up.Close()
		obs.Reject(metrics.RejectInternal)
		s.log.Error("tunnel hijack", "err", err)
		proxy.WriteJSON(w, http.StatusInternalServerError, "internal", "connection cannot be tunneled")
		return
	}
	// The client may have pipelined bytes (the TLS ClientHello) right behind the
	// CONNECT; they sit in the server's read buffer and belong to the upstream.
	var pending []byte
	br := bufrw.Reader
	if n := br.Buffered(); n > 0 {
		pending, _ = br.Peek(n)
	}

	if !s.track(client, up) {
		obs.Reject(metrics.RejectShuttingDown)
		obs.SetStatus(http.StatusServiceUnavailable)
		_, _ = client.Write([]byte("HTTP/1.1 503 Service Unavailable\r\nConnection: close\r\nContent-Length: 0\r\n\r\n"))
		_ = client.Close()
		_ = up.Close()
		return
	}
	defer s.untrack(client, up)
	obs.TunnelOpen()
	defer func() { _ = client.Close() }()
	defer func() { _ = up.Close() }()

	_ = client.SetDeadline(time.Time{}) // drop ReadHeaderTimeout; relay sets its own
	if _, err := client.Write([]byte(established)); err != nil {
		return
	}
	obs.AddBytes(s.relay(client, up, pending))
}

func (s *Server) portAllowed(port int) bool {
	_, ok := s.ports[port]
	return ok
}

// acquire takes a tunnel slot without blocking; false means the cap is reached.
func (s *Server) acquire() bool {
	if s.slots == nil {
		return true
	}
	select {
	case s.slots <- struct{}{}:
		return true
	default:
		return false
	}
}

func (s *Server) release() {
	if s.slots != nil {
		<-s.slots
	}
}

func (s *Server) isClosing() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closing
}

func (s *Server) track(a, b net.Conn) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closing {
		return false
	}
	s.conns[a], s.conns[b] = struct{}{}, struct{}{}
	s.wg.Add(1)
	return true
}

func (s *Server) untrack(a, b net.Conn) {
	s.mu.Lock()
	delete(s.conns, a)
	delete(s.conns, b)
	s.mu.Unlock()
	s.wg.Done()
}

// relay copies both directions until each side is done and returns the bytes copied
// (the client's pipelined bytes included). The idle deadline on both conns moves
// forward on traffic in either direction, so a long download with a silent client
// stays open.
func (s *Server) relay(client, up net.Conn, pending []byte) int64 {
	extend := func() {
		d := time.Now().Add(s.idle)
		_ = client.SetDeadline(d)
		_ = up.SetDeadline(d)
	}
	extend()
	var total int64
	if len(pending) > 0 {
		n, err := up.Write(pending)
		total += int64(n)
		if err != nil {
			return total
		}
	}

	closeBoth := func() {
		_ = client.Close()
		_ = up.Close()
	}
	var wg sync.WaitGroup
	var toUp, toClient int64 // each written by its own goroutine, read after wg.Wait
	wg.Add(2)
	go func() {
		defer wg.Done()
		toUp = pipe(up, client, extend, closeBoth)
	}()
	go func() {
		defer wg.Done()
		toClient = pipe(client, up, extend, closeBoth)
	}()
	wg.Wait()
	return total + toUp + toClient
}

// pipe copies src to dst and returns the bytes written. On EOF it half-closes dst
// (when supported) so the peer can still answer; any other end, or a conn that cannot
// half-close, tears both down.
func pipe(dst, src net.Conn, extend, closeBoth func()) (written int64) {
	buf := make([]byte, copyBufBytes)
	for {
		n, err := src.Read(buf)
		if n > 0 {
			extend()
			w, werr := dst.Write(buf[:n])
			written += int64(w)
			if werr != nil {
				closeBoth()
				return written
			}
			extend()
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				if cw, ok := dst.(interface{ CloseWrite() error }); ok && cw.CloseWrite() == nil {
					return written
				}
			}
			closeBoth()
			return written
		}
	}
}

// proxyToken extracts the token from `Proxy-Authorization: Basic base64(user:token)`.
// The username is ignored; any malformed value yields an empty token.
func proxyToken(h http.Header) string {
	scheme, cred, ok := strings.Cut(strings.TrimSpace(h.Get("Proxy-Authorization")), " ")
	if !ok || !strings.EqualFold(scheme, "Basic") {
		return ""
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(cred))
	if err != nil {
		return ""
	}
	_, token, _ := strings.Cut(string(raw), ":")
	return token
}
