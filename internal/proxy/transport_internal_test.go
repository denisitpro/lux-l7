package proxy

import (
	"testing"
	"time"

	"github.com/denisitpro/lux-l7/internal/config"
)

func TestTransportPoolAndTimeout(t *testing.T) {
	tr := newTransport(config.Config{UpstreamTimeout: 7 * time.Second})
	if tr.MaxIdleConnsPerHost != 64 {
		t.Fatalf("MaxIdleConnsPerHost = %d, want 64", tr.MaxIdleConnsPerHost)
	}
	if tr.ResponseHeaderTimeout != 7*time.Second {
		t.Fatalf("ResponseHeaderTimeout = %v, want 7s", tr.ResponseHeaderTimeout)
	}
}
