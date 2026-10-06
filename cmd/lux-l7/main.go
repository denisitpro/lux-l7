package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/denisitpro/lux-l7/internal/auth"
	"github.com/denisitpro/lux-l7/internal/config"
	"github.com/denisitpro/lux-l7/internal/front"
	"github.com/denisitpro/lux-l7/internal/proxy"
	"github.com/denisitpro/lux-l7/internal/relay"
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

	logger.Info("starting", "version", version.Build)

	var appHandler http.Handler
	switch cfg.ProxyMode {
	case config.ProxyModeRoute:
		logger.Info("proxy mode", "mode", "route", "routes", len(cfg.Routes))
		appHandler = relay.New(cfg, cfg.Routes, logger, relay.Options{})
	default:
		logger.Info("proxy mode", "mode", "header")
		authValidator, err := auth.New(cfg)
		if err != nil {
			logger.Error("auth", "err", err)
			os.Exit(1)
		}
		appHandler = proxy.New(cfg, authValidator, logger)
	}

	if cfg.ProxyMode == config.ProxyModeHeader && !cfg.AuthEnabled {
		logger.Warn("auth disabled: do not expose this configuration on the public internet")
	}

	srv := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           front.New(appHandler),
		ReadHeaderTimeout: cfg.ReadHeaderTimeout,
		ReadTimeout:       cfg.ReadTimeout,
		WriteTimeout:      cfg.WriteTimeout,
		IdleTimeout:       cfg.IdleTimeout,
	}

	go func() {
		logger.Info("listening", "addr", cfg.HTTPAddr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("listen", "err", err)
			os.Exit(1)
		}
	}()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	<-ctx.Done()

	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		logger.Error("shutdown", "err", err)
		os.Exit(1)
	}
	logger.Info("stopped")
}
