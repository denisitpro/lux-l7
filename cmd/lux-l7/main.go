package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/denisitpro/lux-l7/internal/auth"
	"github.com/denisitpro/lux-l7/internal/config"
	"github.com/denisitpro/lux-l7/internal/front"
	"github.com/denisitpro/lux-l7/internal/listener"
	"github.com/denisitpro/lux-l7/internal/metrics"
	"github.com/denisitpro/lux-l7/internal/proxy"
	"github.com/denisitpro/lux-l7/internal/relay"
	"github.com/denisitpro/lux-l7/internal/reqlog"
	"github.com/denisitpro/lux-l7/internal/tunnel"
	"github.com/denisitpro/lux-l7/internal/version"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		slog.Error("config", "err", err)
		os.Exit(1)
	}

	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: cfg.LogLevel}))
	slog.SetDefault(logger)

	for _, w := range cfg.Warnings() {
		logger.Warn(w)
	}

	logger.Info("starting", "version", version.Build, "commit", version.Commit, "ref", version.Ref, "built_at", version.BuiltAt)

	// The request log always observes requests; Prometheus joins only when METRICS_ADDR is set.
	reqLog := reqlog.New(logger)
	sinks := []metrics.Sink{reqLog}
	var metricsSrv *metrics.Server
	if cfg.MetricsAddr != "" {
		reg := metrics.NewRegistry()
		sinks = append(sinks, metrics.New(reg, metrics.Options{Location: cfg.MetricsLocation, IngressHosts: cfg.MetricsIngressHosts}))
		metricsSrv, err = metrics.Start(logger, cfg.MetricsAddr, reg)
		if err != nil {
			logger.Error("metrics", "err", err)
			os.Exit(1)
		}
	}
	observer := metrics.NewObserver(sinks...)

	// The periodic log lines run until the servers have drained, then flush once more.
	logCtx, stopLog := context.WithCancel(context.Background())
	defer stopLog()
	ticker := time.NewTicker(reqlog.SummaryInterval)
	defer ticker.Stop()
	logDone := make(chan struct{})
	go func() {
		defer close(logDone)
		reqLog.Run(logCtx, ticker.C)
	}()

	var appHandler http.Handler
	var authValidator *auth.Validator
	switch cfg.ProxyMode {
	case config.ProxyModeRoute:
		logger.Info("proxy mode", "mode", "route", "routes", len(cfg.Routes))
		appHandler = relay.New(cfg, cfg.Routes, logger, relay.Options{Observer: observer})
	default:
		logger.Info("proxy mode", "mode", string(cfg.ProxyMode))
		authValidator, err = auth.New(cfg)
		if err != nil {
			logger.Error("auth", "err", err)
			os.Exit(1)
		}
		if cfg.AuthEnabled && cfg.AuthMode == config.AuthModeInstance {
			logger.Info("auth", "mode", "instance", "tokens", authValidator.InstanceTokenCount())
		}
		appHandler = proxy.New(cfg, authValidator, logger, proxy.WithObserver(observer))
	}

	if cfg.ProxyMode != config.ProxyModeRoute && !cfg.AuthEnabled {
		logger.Warn("auth disabled: do not expose this configuration on the public internet")
	}

	srv, err := listener.New(cfg, front.New(appHandler), logger)
	if err != nil {
		logger.Error("listener", "err", err)
		os.Exit(1)
	}

	go func() {
		logger.Info("listening", "addr", cfg.HTTPAddr, "tls", cfg.HTTPTLSCertFile != "", "h2c", cfg.HTTPH2C && cfg.HTTPTLSCertFile == "")
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("listen", "err", err)
			os.Exit(1)
		}
	}()

	var tun *tunnel.Server
	if cfg.TunnelAddr != "" {
		tun, err = tunnel.New(cfg, authValidator, logger, tunnel.WithObserver(observer))
		if err != nil {
			logger.Error("tunnel", "err", err)
			os.Exit(1)
		}
		go func() {
			logger.Info("tunnel listening", "addr", cfg.TunnelAddr)
			if err := tun.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
				logger.Error("tunnel listen", "err", err)
				os.Exit(1)
			}
		}()
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	<-ctx.Done()

	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer cancel()
	var (
		wg     sync.WaitGroup
		failed bool
		mu     sync.Mutex
	)
	shutdown := func(name string, fn func(context.Context) error) {
		defer wg.Done()
		if err := fn(shutdownCtx); err != nil {
			logger.Error("shutdown", "server", name, "err", err)
			mu.Lock()
			failed = true
			mu.Unlock()
		}
	}
	wg.Add(1)
	go shutdown("http", srv.Shutdown)
	if tun != nil {
		wg.Add(1)
		go shutdown("tunnel", tun.Shutdown)
	}
	if metricsSrv != nil {
		wg.Add(1)
		go shutdown("metrics", metricsSrv.Shutdown)
	}
	wg.Wait()
	stopLog()
	<-logDone
	if failed {
		os.Exit(1)
	}
	logger.Info("stopped")
}
