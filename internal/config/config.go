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
	// Read and write timeouts are off by default: they cap the whole request or
	// response, so they would cut SSE, long downloads, slow uploads and WebSocket.
	defaultReadTimeout       = 0
	defaultWriteTimeout      = 0
	defaultIdleTimeout       = 120 * time.Second
	defaultShutdownTimeout   = 15 * time.Second
	defaultUpstreamTimeout   = 30 * time.Second
	defaultUpstreamMaxBytes  = 32 << 20
	defaultUpstreamScheme    = "https"
	defaultUpstreamPorts     = "80,443,8443,9443"
	defaultTunnelPorts       = "443"
	defaultTunnelIdleTimeout = 10 * time.Minute
	// defaultTunnelMaxConns stays below typical OS and Kubernetes file-descriptor
	// limits: every tunnel holds two sockets.
	defaultTunnelMaxConns = 300
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
	// UpstreamAllowPrivate disables the SSRF guard (header mode and tunnel): loopback, private,
	// link-local and other non-public resolved IPs become reachable. Local tests only.
	UpstreamAllowPrivate bool
	// UpstreamAllowPorts lists the target ports a header-mode client may reach.
	UpstreamAllowPorts []int

	AllowHosts    []string
	AllowSuffixes []string
	DenyHosts     []string

	AuthEnabled   bool
	AuthMode      AuthMode
	InstanceToken string
	HostTokenJSON string

	// IgnoredAuthVars names the removed AUTH_ENABLED / AUTH_MODE when present in the
	// environment; Warnings reports them. Their values are never read.
	IgnoredAuthVars []string

	// TunnelAddr is the CONNECT tunnel listen address; empty disables the tunnel.
	TunnelAddr string
	// TunnelAllowPorts lists the target ports a CONNECT client may reach.
	TunnelAllowPorts []int
	// TunnelIdleTimeout closes a tunnel after this long without traffic in either direction.
	TunnelIdleTimeout time.Duration
	// TunnelMaxConnections caps concurrent tunnels; 0 means unlimited.
	TunnelMaxConnections int
	// TunnelTLSCertFile and TunnelTLSKeyFile enable TLS on the tunnel listener (both or neither).
	TunnelTLSCertFile string
	TunnelTLSKeyFile  string

	// MetricsAddr is the Prometheus listener (full bind address); empty disables metrics.
	MetricsAddr string
	// MetricsLocation is a constant `location` label on every series; empty adds none.
	MetricsLocation string
	// MetricsIngressHosts are the front hosts header mode may label; others are "other".
	MetricsIngressHosts []string
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
			return Config{}, inEveryMode(fmt.Errorf("ROUTE_MAX_HOPS: %w", err))
		}
		routeMaxHops = n
	}

	allowHTTP, err := envBool("UPSTREAM_ALLOW_HTTP", false)
	if err != nil {
		return Config{}, inEveryMode(err)
	}
	allowPrivate, err := envBool("UPSTREAM_ALLOW_PRIVATE", false)
	if err != nil {
		return Config{}, inEveryMode(err)
	}
	ports, err := parsePorts("UPSTREAM_ALLOW_PORTS", envOr("UPSTREAM_ALLOW_PORTS", defaultUpstreamPorts))
	if err != nil {
		return Config{}, inEveryMode(err)
	}
	tunnelPorts, err := parsePorts("TUNNEL_ALLOW_PORTS", envOr("TUNNEL_ALLOW_PORTS", defaultTunnelPorts))
	if err != nil {
		return Config{}, inEveryMode(err)
	}

	tunnelMaxConns, err := envNonNegInt("TUNNEL_MAX_CONNECTIONS", defaultTunnelMaxConns)
	if err != nil {
		return Config{}, inEveryMode(err)
	}

	cfg := Config{
		HTTPAddr:             envOr("HTTP_ADDR", defaultHTTPAddr),
		ProxyMode:            mode,
		RouteMaxHops:         routeMaxHops,
		ReadHeaderTimeout:    defaultReadHeaderTimeout,
		ReadTimeout:          defaultReadTimeout,
		WriteTimeout:         defaultWriteTimeout,
		IdleTimeout:          defaultIdleTimeout,
		ShutdownTimeout:      defaultShutdownTimeout,
		UpstreamTimeout:      defaultUpstreamTimeout,
		UpstreamMaxBytes:     defaultUpstreamMaxBytes,
		UpstreamScheme:       strings.ToLower(envOr("UPSTREAM_DEFAULT_SCHEME", defaultUpstreamScheme)),
		AllowHosts:           splitCSV(envOr("UPSTREAM_ALLOW_HOSTS", "")),
		AllowSuffixes:        splitCSV(envOr("UPSTREAM_ALLOW_SUFFIXES", "")),
		DenyHosts:            splitCSV(envOr("UPSTREAM_DENY_HOSTS", "")),
		UpstreamAllowHTTP:    allowHTTP,
		UpstreamAllowPrivate: allowPrivate,
		UpstreamAllowPorts:   ports,
		AuthMode:             AuthModeInstance,
		InstanceToken:        strings.TrimSpace(os.Getenv("PROXY_AUTH_TOKEN")),
		HostTokenJSON:        strings.TrimSpace(os.Getenv("PROXY_AUTH_HOST_MAP")),
		TunnelAddr:           strings.TrimSpace(os.Getenv("TUNNEL_ADDR")),
		TunnelAllowPorts:     tunnelPorts,
		TunnelIdleTimeout:    defaultTunnelIdleTimeout,
		TunnelMaxConnections: tunnelMaxConns,
		TunnelTLSCertFile:    strings.TrimSpace(os.Getenv("TUNNEL_TLS_CERT_FILE")),
		TunnelTLSKeyFile:     strings.TrimSpace(os.Getenv("TUNNEL_TLS_KEY_FILE")),
		MetricsAddr:          strings.TrimSpace(os.Getenv("METRICS_ADDR")),
		MetricsLocation:      strings.TrimSpace(os.Getenv("METRICS_LOCATION")),
		MetricsIngressHosts:  splitCSV(os.Getenv("METRICS_INGRESS_HOSTS")),
	}

	level, err := parseLevel(envOr("LOG_LEVEL", defaultLogLevel))
	if err != nil {
		return Config{}, err
	}
	cfg.LogLevel = level

	if err := fillDurations(&cfg); err != nil {
		return Config{}, err
	}
	if err := resolveAuth(&cfg); err != nil {
		return Config{}, err
	}
	for _, k := range removedAuthVars {
		if _, ok := os.LookupEnv(k); ok {
			cfg.IgnoredAuthVars = append(cfg.IgnoredAuthVars, k)
		}
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
		{"TUNNEL_IDLE_TIMEOUT", &cfg.TunnelIdleTimeout},
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
	// Go's http.Server treats zero or negative as "no limit": the slow-header and
	// keep-alive bounds would vanish silently.
	if cfg.ReadHeaderTimeout <= 0 {
		return fmt.Errorf("HTTP_READ_HEADER_TIMEOUT must be > 0, got %s", cfg.ReadHeaderTimeout)
	}
	if cfg.IdleTimeout <= 0 {
		return fmt.Errorf("HTTP_IDLE_TIMEOUT must be > 0, got %s", cfg.IdleTimeout)
	}
	if cfg.ReadTimeout < 0 {
		return fmt.Errorf("HTTP_READ_TIMEOUT must be >= 0, got %s", cfg.ReadTimeout)
	}
	if cfg.WriteTimeout < 0 {
		return fmt.Errorf("HTTP_WRITE_TIMEOUT must be >= 0, got %s", cfg.WriteTimeout)
	}
	if cfg.TunnelIdleTimeout <= 0 {
		return fmt.Errorf("TUNNEL_IDLE_TIMEOUT must be > 0, got %s", cfg.TunnelIdleTimeout)
	}
	switch cfg.ProxyMode {
	case ProxyModeHeader, ProxyModeRoute:
	default:
		return fmt.Errorf("PROXY_MODE must be header or route, got %q", cfg.ProxyMode)
	}
	if cfg.RouteMaxHops < 1 {
		return inEveryMode(fmt.Errorf("ROUTE_MAX_HOPS must be >= 1, got %d", cfg.RouteMaxHops))
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
		return inEveryMode(fmt.Errorf("UPSTREAM_DEFAULT_SCHEME must be http or https, got %q", cfg.UpstreamScheme))
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
	if err := validateTunnel(cfg); err != nil {
		return err
	}
	return validateMetrics(cfg)
}

// inEveryMode marks an error from a mode-specific variable: these are parsed in
// every mode (fail fast), so the error is not a sign the variable matters here.
func inEveryMode(err error) error {
	return fmt.Errorf("%w (checked in every PROXY_MODE, even where the variable has no effect)", err)
}

// Warnings lists non-fatal config notes. main logs them through slog once the JSON
// handler is installed; emitting them from Load would use the unformatted default logger.
func (c Config) Warnings() []string {
	var w []string
	state := "off"
	if c.AuthEnabled {
		state = "on (" + string(c.AuthMode) + ")"
	}
	for _, k := range c.IgnoredAuthVars {
		w = append(w, k+" is ignored (removed): auth is "+state+", it follows PROXY_AUTH_TOKEN / PROXY_AUTH_HOST_MAP")
	}
	if c.ProxyMode == ProxyModeRoute && (len(c.AllowHosts) > 0 || len(c.AllowSuffixes) > 0 || len(c.DenyHosts) > 0) {
		w = append(w, "UPSTREAM_ALLOW_HOSTS, UPSTREAM_ALLOW_SUFFIXES and UPSTREAM_DENY_HOSTS are ignored in route mode")
	}
	if c.ProxyMode == ProxyModeHeader && len(c.AllowHosts) == 0 && len(c.AllowSuffixes) == 0 {
		w = append(w, "upstream allow-list empty: any host may be targeted unless UPSTREAM_DENY_HOSTS blocks it")
	}
	if c.UpstreamAllowPrivate && (c.ProxyMode == ProxyModeHeader || c.TunnelAddr != "") {
		w = append(w, "UPSTREAM_ALLOW_PRIVATE=true: SSRF guard off, loopback, private and metadata upstreams are reachable")
	}
	if c.MetricsAddr == "" && (c.MetricsLocation != "" || len(c.MetricsIngressHosts) > 0) {
		w = append(w, "METRICS_LOCATION and METRICS_INGRESS_HOSTS have no effect while METRICS_ADDR is empty")
	}
	if c.ProxyMode == ProxyModeRoute && len(c.MetricsIngressHosts) > 0 {
		w = append(w, "METRICS_INGRESS_HOSTS is ignored in route mode: ingress_host comes from ROUTES")
	}
	return w
}

// validateTunnel rejects tunnel settings that would expose an open CONNECT relay or
// a half-configured TLS listener.
func validateTunnel(cfg *Config) error {
	if (cfg.TunnelTLSCertFile == "") != (cfg.TunnelTLSKeyFile == "") {
		return fmt.Errorf("TUNNEL_TLS_CERT_FILE and TUNNEL_TLS_KEY_FILE must be set together")
	}
	if cfg.TunnelAddr == "" {
		return nil
	}
	if cfg.ProxyMode == ProxyModeRoute {
		return fmt.Errorf("TUNNEL_ADDR is not supported when PROXY_MODE=route")
	}
	if sameListenAddr(cfg.TunnelAddr, cfg.HTTPAddr) {
		return fmt.Errorf("TUNNEL_ADDR %q clashes with HTTP_ADDR %q: one socket per listener", cfg.TunnelAddr, cfg.HTTPAddr)
	}
	if !cfg.AuthEnabled && len(cfg.AllowHosts) == 0 && len(cfg.AllowSuffixes) == 0 {
		return fmt.Errorf("TUNNEL_ADDR without auth requires UPSTREAM_ALLOW_HOSTS or UPSTREAM_ALLOW_SUFFIXES: an open CONNECT relay is refused")
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
// such as UPSTREAM_ALLOW_PRIVATE=yes into "guard on".
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

// envNonNegInt parses an integer >= 0; empty yields fallback. Zero is a valid value
// (the caller decides what it means), a negative or non-integer one is an error.
func envNonNegInt(key string, fallback int) (int, error) {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 0 {
		return 0, fmt.Errorf("%s: invalid value %q (use an integer >= 0)", key, raw)
	}
	return n, nil
}

func parsePorts(name, raw string) ([]int, error) {
	var out []int
	for _, p := range splitCSV(raw) {
		n, err := strconv.ParseUint(p, 10, 16)
		if err != nil || n == 0 {
			return nil, fmt.Errorf("%s: invalid port %q", name, p)
		}
		out = append(out, int(n))
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("%s: no ports", name)
	}
	return out, nil
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
