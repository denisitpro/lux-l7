// Package listener builds the main HTTP server: plain HTTP/1.1, optional TLS from a
// reloadable PEM pair (HTTP/2 negotiated through ALPN), or opt-in cleartext HTTP/2
// with prior knowledge (h2c).
package listener

import (
	"context"
	"crypto/tls"
	"log/slog"
	"net"
	"net/http"

	"github.com/denisitpro/lux-l7/internal/certs"
	"github.com/denisitpro/lux-l7/internal/config"
)

// Server wraps the main http.Server together with its TLS settings.
type Server struct {
	srv    *http.Server
	tlsCfg *tls.Config // nil: cleartext listener
}

// New builds the server for h. It loads the TLS pair when configured, so an
// unreadable or mismatched pair fails startup.
func New(cfg config.Config, h http.Handler, log *slog.Logger) (*Server, error) {
	if log == nil {
		log = slog.Default()
	}
	s := &Server{srv: &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           h,
		ReadHeaderTimeout: cfg.ReadHeaderTimeout,
		ReadTimeout:       cfg.ReadTimeout,
		WriteTimeout:      cfg.WriteTimeout,
		IdleTimeout:       cfg.IdleTimeout,
	}}

	var protocols http.Protocols
	switch {
	case cfg.HTTPTLSCertFile != "" || cfg.HTTPTLSKeyFile != "":
		r, err := certs.NewReloader("http tls", cfg.HTTPTLSCertFile, cfg.HTTPTLSKeyFile, log)
		if err != nil {
			return nil, err
		}
		s.tlsCfg = &tls.Config{
			MinVersion:     tls.VersionTLS12,
			NextProtos:     []string{"h2", "http/1.1"},
			GetCertificate: r.GetCertificate,
		}
		s.srv.TLSConfig = s.tlsCfg
		protocols.SetHTTP1(true)
		protocols.SetHTTP2(true)
	case cfg.HTTPH2C:
		protocols.SetHTTP1(true)
		protocols.SetUnencryptedHTTP2(true)
	default:
		protocols.SetHTTP1(true)
	}
	s.srv.Protocols = &protocols
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

// Serve serves on l (as TLS when a certificate is configured).
func (s *Server) Serve(l net.Listener) error {
	if s.tlsCfg != nil {
		// Certificates come from TLSConfig.GetCertificate, not from files here.
		return s.srv.ServeTLS(l, "", "")
	}
	return s.srv.Serve(l)
}

// Shutdown stops accepting and waits for in-flight requests until ctx ends.
func (s *Server) Shutdown(ctx context.Context) error {
	return s.srv.Shutdown(ctx)
}
