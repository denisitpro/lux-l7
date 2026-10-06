// Package config loads process settings from the environment.
// Optional test.env is loaded when present and never overrides existing env vars.
package config

import (
	"fmt"
	"log/slog"
	"net"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/joho/godotenv"

	"github.com/denisitpro/lux-l7/internal/route"
)

const (
	defaultProxyMode         = "header"
	defaultRouteMaxHops      = 1
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

// ProxyMode selects header-based or static route-table forwarding.
type ProxyMode string

const (
	ProxyModeHeader ProxyMode = "header"
	ProxyModeRoute  ProxyMode = "route"
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

	ProxyMode    ProxyMode
	Routes       []route.Route
	RouteMaxHops int

	UpstreamTimeout  time.Duration
	UpstreamMaxBytes int64
	UpstreamScheme   string
	// UpstreamAllowHTTP permits plain-http upstreams in header mode (default scheme or
	// X-L7-Target-Scheme). Off by default so a client cannot downgrade the upstream hop.
	UpstreamAllowHTTP bool

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

	mode := ProxyMode(strings.ToLower(envOr("PROXY_MODE", defaultProxyMode)))
	routesRaw := strings.TrimSpace(os.Getenv("ROUTES"))
	routeMaxHops := defaultRouteMaxHops
	if raw := strings.TrimSpace(os.Getenv("ROUTE_MAX_HOPS")); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil {
			return Config{}, fmt.Errorf("ROUTE_MAX_HOPS: %w", err)
		}
		routeMaxHops = n
	}

	authEnabled, err := envBool("AUTH_ENABLED", false)
	if err != nil {
		return Config{}, err
	}
	allowHTTP, err := envBool("UPSTREAM_ALLOW_HTTP", false)
	if err != nil {
		return Config{}, err
	}

	cfg := Config{
		HTTPAddr:          envOr("HTTP_ADDR", defaultHTTPAddr),
		ProxyMode:         mode,
		RouteMaxHops:      routeMaxHops,
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
		UpstreamAllowHTTP: allowHTTP,
		AuthEnabled:       authEnabled,
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
	if err := validate(&cfg, routesRaw); err != nil {
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

func validate(cfg *Config, routesRaw string) error {
	// Zero or negative values would silently disable the limit or time out at once.
	if cfg.UpstreamMaxBytes <= 0 {
		return fmt.Errorf("UPSTREAM_MAX_BYTES must be > 0, got %d", cfg.UpstreamMaxBytes)
	}
	if cfg.UpstreamTimeout <= 0 {
		return fmt.Errorf("UPSTREAM_TIMEOUT must be > 0, got %s", cfg.UpstreamTimeout)
	}
	if cfg.ShutdownTimeout <= 0 {
		return fmt.Errorf("SHUTDOWN_TIMEOUT must be > 0, got %s", cfg.ShutdownTimeout)
	}
	switch cfg.ProxyMode {
	case ProxyModeHeader, ProxyModeRoute:
	default:
		return fmt.Errorf("PROXY_MODE must be header or route, got %q", cfg.ProxyMode)
	}
	if cfg.RouteMaxHops < 1 {
		return fmt.Errorf("ROUTE_MAX_HOPS must be >= 1, got %d", cfg.RouteMaxHops)
	}
	if cfg.ProxyMode == ProxyModeHeader {
		if routesRaw != "" {
			return fmt.Errorf("ROUTES must not be set when PROXY_MODE=header")
		}
	} else {
		if routesRaw == "" {
			return fmt.Errorf("ROUTES is required when PROXY_MODE=route")
		}
		routes, err := route.ParseJSON(routesRaw)
		if err != nil {
			return err
		}
		cfg.Routes = routes
	}

	switch cfg.UpstreamScheme {
	case "http", "https":
	default:
		return fmt.Errorf("UPSTREAM_DEFAULT_SCHEME must be http or https, got %q", cfg.UpstreamScheme)
	}
	if cfg.ProxyMode == ProxyModeHeader {
		if cfg.UpstreamScheme == "http" && !cfg.UpstreamAllowHTTP {
			return fmt.Errorf("UPSTREAM_DEFAULT_SCHEME=http requires UPSTREAM_ALLOW_HTTP=true")
		}
		// Policy compares bare hostnames: an entry with a port would never match.
		for _, key := range []struct {
			name    string
			entries []string
		}{
			{"UPSTREAM_ALLOW_HOSTS", cfg.AllowHosts},
			{"UPSTREAM_ALLOW_SUFFIXES", cfg.AllowSuffixes},
			{"UPSTREAM_DENY_HOSTS", cfg.DenyHosts},
		} {
			for _, e := range key.entries {
				if _, _, err := net.SplitHostPort(e); err == nil {
					return fmt.Errorf("%s: entry %q must be a bare hostname without a port", key.name, e)
				}
			}
		}
	}
	switch cfg.AuthMode {
	case AuthModeInstance, AuthModePerHost:
	default:
		return fmt.Errorf("AUTH_MODE must be instance or per_host, got %q", cfg.AuthMode)
	}
	if cfg.ProxyMode == ProxyModeRoute && cfg.AuthEnabled {
		return fmt.Errorf("auth is not supported in route mode: unset AUTH_ENABLED")
	}
	if cfg.AuthEnabled && cfg.AuthMode == AuthModeInstance && cfg.InstanceToken == "" {
		return fmt.Errorf("AUTH_ENABLED with AUTH_MODE=instance requires PROXY_AUTH_TOKEN")
	}
	if cfg.AuthEnabled && cfg.AuthMode == AuthModePerHost && cfg.HostTokenJSON == "" {
		return fmt.Errorf("AUTH_ENABLED with AUTH_MODE=per_host requires PROXY_AUTH_HOST_MAP")
	}
	if cfg.ProxyMode == ProxyModeRoute && (len(cfg.AllowHosts) > 0 || len(cfg.AllowSuffixes) > 0 || len(cfg.DenyHosts) > 0) {
		slog.Default().Warn("UPSTREAM_ALLOW_HOSTS, UPSTREAM_ALLOW_SUFFIXES and UPSTREAM_DENY_HOSTS are ignored in route mode")
	}
	if cfg.ProxyMode == ProxyModeHeader && len(cfg.AllowHosts) == 0 && len(cfg.AllowSuffixes) == 0 {
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

// envBool fails on an unparsable value: silently falling back would turn a typo
// such as AUTH_ENABLED=yes into "auth off".
func envBool(key string, fallback bool) (bool, error) {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback, nil
	}
	b, err := strconv.ParseBool(raw)
	if err != nil {
		return false, fmt.Errorf("%s: invalid boolean %q (use true or false)", key, raw)
	}
	return b, nil
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
