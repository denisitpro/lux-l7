package config

import (
	"reflect"
	"strings"
	"testing"
)

func TestLoadMetricsSettings(t *testing.T) {
	cases := []struct {
		name    string
		env     map[string]string
		errPart string
	}{
		{"all unset", nil, ""},
		{"addr all interfaces", map[string]string{"METRICS_ADDR": ":9090"}, ""},
		{"addr host", map[string]string{"METRICS_ADDR": "127.0.0.1:9090"}, ""},
		{"addr no port", map[string]string{"METRICS_ADDR": "127.0.0.1"}, "METRICS_ADDR"},
		{"addr bad port", map[string]string{"METRICS_ADDR": ":http"}, "METRICS_ADDR"},
		{"addr equals http", map[string]string{"METRICS_ADDR": ":8080"}, "must differ from HTTP_ADDR"},
		{"addr equals tunnel", map[string]string{"METRICS_ADDR": ":8443", "TUNNEL_ADDR": ":8443", "UPSTREAM_ALLOW_HOSTS": "example.com"}, "must differ from TUNNEL_ADDR"},
		{"location ok", map[string]string{"METRICS_LOCATION": "eu-1"}, ""},
		{"location with space", map[string]string{"METRICS_LOCATION": "eu 1"}, "METRICS_LOCATION"},
		{"location quote", map[string]string{"METRICS_LOCATION": `eu"1`}, "METRICS_LOCATION"},
		{"location too long", map[string]string{"METRICS_LOCATION": strings.Repeat("a", 33)}, "METRICS_LOCATION"},
		{"ingress ok", map[string]string{"METRICS_INGRESS_HOSTS": "Front.example.com, b.example.com"}, ""},
		{"ingress with port", map[string]string{"METRICS_INGRESS_HOSTS": "front.example.com:443"}, "METRICS_INGRESS_HOSTS"},
		{"ingress with slash", map[string]string{"METRICS_INGRESS_HOSTS": "example.com/x"}, "METRICS_INGRESS_HOSTS"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for _, k := range []string{"METRICS_ADDR", "METRICS_LOCATION", "METRICS_INGRESS_HOSTS"} {
				t.Setenv(k, "")
			}
			for k, v := range tc.env {
				t.Setenv(k, v)
			}
			_, err := Load()
			if tc.errPart == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.errPart) {
				t.Fatalf("err %v, want mention of %q", err, tc.errPart)
			}
		})
	}
}

func TestLoadMetricsValues(t *testing.T) {
	t.Setenv("METRICS_ADDR", " 127.0.0.1:9090 ")
	t.Setenv("METRICS_LOCATION", "eu-1")
	t.Setenv("METRICS_INGRESS_HOSTS", "Front.example.com, b.example.com")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.MetricsAddr != "127.0.0.1:9090" || cfg.MetricsLocation != "eu-1" ||
		!reflect.DeepEqual(cfg.MetricsIngressHosts, []string{"front.example.com", "b.example.com"}) {
		t.Fatalf("got %q %q %v", cfg.MetricsAddr, cfg.MetricsLocation, cfg.MetricsIngressHosts)
	}
}

func TestMetricsOffByDefault(t *testing.T) {
	t.Setenv("METRICS_ADDR", "")
	cfg, err := Load()
	if err != nil || cfg.MetricsAddr != "" {
		t.Fatalf("addr %q err %v", cfg.MetricsAddr, err)
	}
}

func TestMetricsWarnings(t *testing.T) {
	c := Config{MetricsLocation: "eu-1"}
	if w := c.Warnings(); !strings.Contains(strings.Join(w, "|"), "no effect while METRICS_ADDR is empty") {
		t.Fatalf("warnings %v", w)
	}
	c = Config{MetricsAddr: ":9090", ProxyMode: ProxyModeRoute, MetricsIngressHosts: []string{"a.example.com"}}
	if w := c.Warnings(); !strings.Contains(strings.Join(w, "|"), "ignored in route mode") {
		t.Fatalf("warnings %v", w)
	}
}
