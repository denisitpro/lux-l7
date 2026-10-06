package config

import (
	"fmt"
	"net"
	"regexp"
	"strconv"
)

// metricsLocationRe is a short token that is safe as a label value and in dashboards.
var metricsLocationRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,31}$`)

// metricsHostRe is a bare lowercase hostname (splitCSV lowercases the list).
var metricsHostRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9._-]*[a-z0-9])?$`)

// validateMetrics checks the observability settings; all are optional.
func validateMetrics(cfg *Config) error {
	if cfg.MetricsAddr != "" {
		if err := validateMetricsAddr(cfg); err != nil {
			return fmt.Errorf("METRICS_ADDR: %w", err)
		}
	}
	if cfg.MetricsLocation != "" && !metricsLocationRe.MatchString(cfg.MetricsLocation) {
		return fmt.Errorf("METRICS_LOCATION: %q must be 1-32 characters of letters, digits, '.', '_' or '-', starting with a letter or digit", cfg.MetricsLocation)
	}
	for _, h := range cfg.MetricsIngressHosts {
		if !metricsHostRe.MatchString(h) {
			return fmt.Errorf("METRICS_INGRESS_HOSTS: entry %q must be a bare hostname without a port", h)
		}
	}
	return nil
}

// validateMetricsAddr checks host:port with the port required and numeric (":9090",
// "127.0.0.1:9090"; the host is optional). It must not equal HTTP_ADDR or TUNNEL_ADDR:
// /metrics never shares a proxy socket.
func validateMetricsAddr(cfg *Config) error {
	_, port, err := net.SplitHostPort(cfg.MetricsAddr)
	if err != nil {
		return err
	}
	if _, err := strconv.ParseUint(port, 10, 16); err != nil {
		return fmt.Errorf("port %q is not a number from 0 to 65535", port)
	}
	if cfg.MetricsAddr == cfg.HTTPAddr {
		return fmt.Errorf("must differ from HTTP_ADDR")
	}
	if cfg.TunnelAddr != "" && cfg.MetricsAddr == cfg.TunnelAddr {
		return fmt.Errorf("must differ from TUNNEL_ADDR")
	}
	return nil
}
