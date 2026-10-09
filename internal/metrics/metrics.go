// Package metrics is the Prometheus instrumentation of lux-l7 (#1). Series live in a
// dedicated registry that the caller owns, never the global default, so tests stay
// hermetic. /metrics is served only by Server, on METRICS_ADDR; the proxy and tunnel
// listeners never expose it. Request observation (Observer, Observation, Sink) is
// separate from this export: Metrics is one Sink, the request log (internal/reqlog)
// another. A nil *Observer or *Observation records nothing, so the instrumented
// code needs no "observing on?" branches. Host labels are bounded by
// construction, see Hosts.
package metrics

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/denisitpro/lux-l7/internal/bind"
	"github.com/denisitpro/lux-l7/internal/discardlog"
)

const namespace = "lux_l7"

// NewRegistry returns a fresh registry with the Go runtime and process collectors.
func NewRegistry() *prometheus.Registry {
	reg := prometheus.NewRegistry()
	reg.MustRegister(collectors.NewGoCollector(), collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))
	return reg
}

// Handler serves reg in the Prometheus text format.
func Handler(reg *prometheus.Registry) http.Handler {
	return promhttp.HandlerFor(reg, promhttp.HandlerOpts{})
}

// Server is the separate metrics listener. It has its own timeouts and is
// stopped by the process on shutdown.
type Server struct {
	http *http.Server
	ln   net.Listener
	done chan struct{}
}

// Start binds addr now (so a taken port fails at startup, and "127.0.0.1:0"
// resolves in tests), then serves only GET /metrics in the background. An empty
// addr means metrics are off: it returns (nil, nil), and a nil *Server is safe
// to Addr and Shutdown. Serve errors are logged: a dead metrics port must not
// take the proxy down.
func Start(log *slog.Logger, addr string, reg *prometheus.Registry) (*Server, error) {
	if addr == "" {
		return nil, nil
	}
	ln, err := bind.Listen(addr)
	if err != nil {
		return nil, err
	}
	mux := http.NewServeMux()
	mux.Handle("GET /metrics", Handler(reg))
	s := &Server{
		ln:   ln,
		done: make(chan struct{}),
		http: &http.Server{
			Handler:           mux,
			ReadHeaderTimeout: 5 * time.Second,
			ReadTimeout:       10 * time.Second,
			WriteTimeout:      30 * time.Second,
			IdleTimeout:       60 * time.Second,
			ErrorLog:          discardlog.New(),
		},
	}
	log.Info("metrics listening", "addr", ln.Addr().String())
	go func() {
		defer close(s.done)
		if err := s.http.Serve(ln); !errors.Is(err, http.ErrServerClosed) {
			log.Error("metrics server", "err", err)
		}
	}()
	return s, nil
}

// Addr is the address actually bound, or nil when metrics are off.
func (s *Server) Addr() net.Addr {
	if s == nil {
		return nil
	}
	return s.ln.Addr()
}

// Shutdown stops accepting and waits for in-flight scrapes and the serve loop.
func (s *Server) Shutdown(ctx context.Context) error {
	if s == nil {
		return nil
	}
	err := s.http.Shutdown(ctx)
	_ = s.ln.Close() // in case Shutdown ran before Serve registered the listener
	<-s.done
	return err
}
