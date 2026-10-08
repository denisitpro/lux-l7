package config_test

import (
	"strings"
	"testing"

	"github.com/denisitpro/lux-l7/internal/config"
)

func routeWarnings(t *testing.T, routes string) string {
	t.Helper()
	t.Setenv("PROXY_MODE", "route")
	t.Setenv("ROUTES", routes)
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	return strings.Join(cfg.Warnings(), "|")
}

// The warning must come back through Warnings (logged by main after the JSON handler
// is installed), not be written during Load by the default text logger.
func TestRouteSNIDifferingFromHostWarns(t *testing.T) {
	got := routeWarnings(t, `[{"host":"example.com","path_prefix":"/","upstream":"https://origin.example.net","tls_server_name":"sni.example.net"}]`)
	for _, want := range []string{"ROUTES[0]", "sni.example.net", "origin.example.net", "differs from the outgoing Host"} {
		if !strings.Contains(got, want) {
			t.Fatalf("warnings %q lack %q", got, want)
		}
	}
}

func TestRouteSNIMatchingHostDoesNotWarn(t *testing.T) {
	got := routeWarnings(t, `[{"host":"example.com","path_prefix":"/","upstream":"https://origin.example.net","tls_server_name":"origin.example.net"}]`)
	if strings.Contains(got, "tls_server_name") {
		t.Fatalf("unexpected warning: %q", got)
	}
}
