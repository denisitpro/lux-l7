package proxy_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/denisitpro/lux-l7/internal/auth"
	"github.com/denisitpro/lux-l7/internal/config"
	"github.com/denisitpro/lux-l7/internal/headers"
	"github.com/denisitpro/lux-l7/internal/proxy"
)

func TestForwardToUpstream(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get(headers.TargetHost) != "" {
			t.Fatal("routing header leaked to upstream")
		}
		if r.URL.Path != "/v1/ping" {
			t.Fatalf("path = %q", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("pong"))
	}))
	t.Cleanup(upstream.Close)

	authority := upstream.Listener.Addr().String()
	cfg := config.Config{
		UpstreamScheme: "http",
		AllowHosts:     []string{"127.0.0.1"},
		AuthEnabled:    false,
	}
	v, err := auth.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	h := proxy.New(cfg, v, nil)

	proxySrv := httptest.NewServer(h)
	t.Cleanup(proxySrv.Close)

	req, err := http.NewRequest(http.MethodGet, proxySrv.URL+"/v1/ping", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set(headers.TargetHost, authority)
	req.Header.Set(headers.TargetScheme, "http")

	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	body, _ := io.ReadAll(res.Body)
	if res.StatusCode != http.StatusOK || string(body) != "pong" {
		t.Fatalf("status=%d body=%q", res.StatusCode, body)
	}
}

func TestMissingTargetHost(t *testing.T) {
	cfg := config.Config{AuthEnabled: false}
	v, _ := auth.New(cfg)
	h := proxy.New(cfg, v, nil)
	proxySrv := httptest.NewServer(h)
	t.Cleanup(proxySrv.Close)

	res, err := http.Get(proxySrv.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d", res.StatusCode)
	}
}
