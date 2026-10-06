package config_test

import (
	"strings"
	"testing"

	"github.com/denisitpro/lux-l7/internal/config"
)

func TestProxyModeRouteRequiresRoutes(t *testing.T) {
	t.Setenv("PROXY_MODE", "route")
	t.Setenv("ROUTES", "")
	_, err := config.Load()
	if err == nil || !strings.Contains(err.Error(), "ROUTES") {
		t.Fatalf("expected ROUTES error, got %v", err)
	}
}

func TestRoutesRejectedInHeaderMode(t *testing.T) {
	t.Setenv("PROXY_MODE", "header")
	t.Setenv("ROUTES", `[{"host":"example.com","path_prefix":"/","upstream":"http://127.0.0.1"}]`)
	_, err := config.Load()
	if err == nil || !strings.Contains(err.Error(), "ROUTES") {
		t.Fatalf("expected ROUTES rejection, got %v", err)
	}
}

func TestInvalidProxyMode(t *testing.T) {
	t.Setenv("PROXY_MODE", "both")
	_, err := config.Load()
	if err == nil || !strings.Contains(err.Error(), "PROXY_MODE") {
		t.Fatalf("got %v", err)
	}
}

func TestRouteMaxHopsValidation(t *testing.T) {
	t.Setenv("PROXY_MODE", "route")
	t.Setenv("ROUTES", `[{"host":"example.com","path_prefix":"/","upstream":"http://127.0.0.1"}]`)
	t.Setenv("ROUTE_MAX_HOPS", "0")
	_, err := config.Load()
	if err == nil || !strings.Contains(err.Error(), "ROUTE_MAX_HOPS") {
		t.Fatalf("got %v", err)
	}
}

func TestRouteModeLoad(t *testing.T) {
	t.Setenv("PROXY_MODE", "route")
	t.Setenv("ROUTES", `[{"host":"example.com","path_prefix":"/location1","upstream":"http://127.0.0.1:9"}]`)
	t.Setenv("ROUTE_MAX_HOPS", "2")
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ProxyMode != config.ProxyModeRoute {
		t.Fatalf("mode=%v", cfg.ProxyMode)
	}
	if len(cfg.Routes) != 1 || cfg.RouteMaxHops != 2 {
		t.Fatalf("routes=%d hops=%d", len(cfg.Routes), cfg.RouteMaxHops)
	}
}

func TestRouteModeRejectsAuth(t *testing.T) {
	t.Setenv("PROXY_MODE", "route")
	t.Setenv("ROUTES", `[{"host":"example.com","path_prefix":"/","upstream":"http://127.0.0.1:9"}]`)
	t.Setenv("PROXY_AUTH_TOKEN", "x")
	_, err := config.Load()
	if err == nil || !strings.Contains(err.Error(), "not supported in route mode") {
		t.Fatalf("got %v", err)
	}
}

func TestRouteModeHTTPSSuffixWithoutHostHeaderLoads(t *testing.T) {
	t.Setenv("PROXY_MODE", "route")
	t.Setenv("ROUTES", `[{"host":".example.com","path_prefix":"/","upstream":"https://origin.example.net:443"}]`)
	if _, err := config.Load(); err != nil {
		t.Fatalf("got %v", err)
	}
}
