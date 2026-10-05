// Package config loads process settings from the environment.
// Optional test.env is loaded when present and never overrides existing env vars.
package config

import (
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/joho/godotenv"
)

const (
	defaultLogLevel          = "info"
	defaultHTTPAddr          = ":8080"
	defaultReadHeaderTimeout = 5 * time.Second
	defaultReadTimeout       = 30 * time.Second
	defaultWriteTimeout      = 30 * time.Second
	defaultIdleTimeout       = 120 * time.Second
	defaultShutdownTimeout   = 15 * time.Second
	defaultUpstreamTimeout   = 30 * time.Second
	defaultUpstreamMaxBytes  = 32 << 20
	defaultUpstreamScheme    = "https"
)

// AuthMode selects how proxy tokens are validated.
type AuthMode string

const (
	AuthModeInstance AuthMode = "instance"
	AuthModePerHost  AuthMode = "per_host"
)

// Config holds runtime settings for the L7 proxy process.
type Config struct {
	HTTPAddr          string
	LogLevel          slog.Level
	ReadHeaderTimeout time.Duration
	ReadTimeout       time.Duration
	WriteTimeout      time.Duration
	IdleTimeout       time.Duration
	ShutdownTimeout   time.Duration

	UpstreamTimeout  time.Duration
	UpstreamMaxBytes int64
	UpstreamScheme   string

	AllowHosts    []string
	AllowSuffixes []string
	DenyHosts     []string

	AuthEnabled   bool
	AuthMode      AuthMode
	InstanceToken string
	HostTokenJSON string
}

// Load reads configuration from the environment after optionally loading test.env.
func Load() (Config, error) {
	if err := loadEnvFile("test.env"); err != nil {
		return Config{}, err
	}

	cfg := Config{
		HTTPAddr:          envOr("HTTP_ADDR", defaultHTTPAddr),
		ReadHeaderTimeout: defaultReadHeaderTimeout,
		ReadTimeout:       defaultReadTimeout,
		WriteTimeout:      defaultWriteTimeout,
		IdleTimeout:       defaultIdleTimeout,
		ShutdownTimeout:   defaultShutdownTimeout,
		UpstreamTimeout:   defaultUpstreamTimeout,
		UpstreamMaxBytes:  defaultUpstreamMaxBytes,
		UpstreamScheme:    strings.ToLower(envOr("UPSTREAM_DEFAULT_SCHEME", defaultUpstreamScheme)),
		AllowHosts:        splitCSV(envOr("UPSTREAM_ALLOW_HOSTS", "")),
		AllowSuffixes:     splitCSV(envOr("UPSTREAM_ALLOW_SUFFIXES", "")),
		DenyHosts:         splitCSV(envOr("UPSTREAM_DENY_HOSTS", "")),
		AuthEnabled:       envBool("AUTH_ENABLED", false),
		AuthMode:          AuthMode(envOr("AUTH_MODE", string(AuthModeInstance))),
		InstanceToken:     strings.TrimSpace(os.Getenv("PROXY_AUTH_TOKEN")),
		HostTokenJSON:     strings.TrimSpace(os.Getenv("PROXY_AUTH_HOST_MAP")),
	}

	level, err := parseLevel(envOr("LOG_LEVEL", defaultLogLevel))
	if err != nil {
		return Config{}, err
	}
	cfg.LogLevel = level

	if err := fillDurations(&cfg); err != nil {
		return Config{}, err
	}
	if err := validate(cfg); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func loadEnvFile(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("stat env file: %w", err)
	}
	if info.IsDir() {
		return fmt.Errorf("env file %s is a directory", path)
	}
	if err := godotenv.Load(path); err != nil {
		return fmt.Errorf("load env file %s: %w", path, err)
	}
	return nil
}

func fillDurations(cfg *Config) error {
	fields := []struct {
		key string
		dst *time.Duration
	}{
		{"HTTP_READ_HEADER_TIMEOUT", &cfg.ReadHeaderTimeout},
		{"HTTP_READ_TIMEOUT", &cfg.ReadTimeout},
		{"HTTP_WRITE_TIMEOUT", &cfg.WriteTimeout},
		{"HTTP_IDLE_TIMEOUT", &cfg.IdleTimeout},
		{"SHUTDOWN_TIMEOUT", &cfg.ShutdownTimeout},
		{"UPSTREAM_TIMEOUT", &cfg.UpstreamTimeout},
	}
	for _, f := range fields {
		raw := strings.TrimSpace(os.Getenv(f.key))
		if raw == "" {
			continue
		}
		d, err := time.ParseDuration(raw)
		if err != nil {
			return fmt.Errorf("%s: %w", f.key, err)
		}
		*f.dst = d
	}
	if raw := strings.TrimSpace(os.Getenv("UPSTREAM_MAX_BYTES")); raw != "" {
		n, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			return fmt.Errorf("UPSTREAM_MAX_BYTES: %w", err)
		}
		cfg.UpstreamMaxBytes = n
	}
	return nil
}

func validate(cfg Config) error {
	switch cfg.UpstreamScheme {
	case "http", "https":
	default:
		return fmt.Errorf("UPSTREAM_DEFAULT_SCHEME must be http or https, got %q", cfg.UpstreamScheme)
	}
	switch cfg.AuthMode {
	case AuthModeInstance, AuthModePerHost:
	default:
		return fmt.Errorf("AUTH_MODE must be instance or per_host, got %q", cfg.AuthMode)
	}
	if cfg.AuthEnabled && cfg.AuthMode == AuthModeInstance && cfg.InstanceToken == "" {
		return fmt.Errorf("AUTH_ENABLED with AUTH_MODE=instance requires PROXY_AUTH_TOKEN")
	}
	if cfg.AuthEnabled && cfg.AuthMode == AuthModePerHost && cfg.HostTokenJSON == "" {
		return fmt.Errorf("AUTH_ENABLED with AUTH_MODE=per_host requires PROXY_AUTH_HOST_MAP")
	}
	if len(cfg.AllowHosts) == 0 && len(cfg.AllowSuffixes) == 0 {
		slog.Default().Warn("upstream allow-list empty: any host may be targeted unless UPSTREAM_DENY_HOSTS blocks it")
	}
	return nil
}

func envOr(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}

func envBool(key string, fallback bool) bool {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback
	}
	b, err := strconv.ParseBool(raw)
	if err != nil {
		return fallback
	}
	return b
}

func splitCSV(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(strings.ToLower(p))
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

func parseLevel(raw string) (slog.Level, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "debug":
		return slog.LevelDebug, nil
	case "info":
		return slog.LevelInfo, nil
	case "warn", "warning":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	default:
		return slog.LevelInfo, fmt.Errorf("unknown LOG_LEVEL %q", raw)
	}
}
