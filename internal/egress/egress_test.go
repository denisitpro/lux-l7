package egress_test

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"testing"
	"time"

	"github.com/denisitpro/lux-l7/internal/egress"
)

func TestBlocked(t *testing.T) {
	blocked := []string{
		"0.0.0.0", "0.1.2.3", "::",
		"127.0.0.1", "127.255.255.254", "::1",
		"10.0.0.1", "172.16.0.1", "172.31.255.255", "192.168.1.1",
		"100.64.0.1", "100.127.255.254",
		"169.254.169.254", "169.254.0.1", "fe80::1", "febf::1",
		"192.0.0.8",
		"198.18.0.1", "198.19.255.255",
		"240.0.0.1", "255.255.255.255",
		"224.0.0.1", "239.255.255.250", "ff02::1", "ff0e::1",
		"fc00::1", "fd12:3456::1",
		// IPv4-mapped IPv6
		"::ffff:127.0.0.1", "::ffff:10.0.0.1", "::ffff:169.254.169.254", "::ffff:0.0.0.0",
		// NAT64 and 6to4 with a private embedded IPv4
		"64:ff9b::7f00:1", "64:ff9b::a9fe:a9fe", "64:ff9b::a00:1",
		"2002:7f00:1::", "2002:a9fe:a9fe::1", "2002:c0a8:101::1",
		"64:ff9b:1::1",
	}
	for _, s := range blocked {
		if !egress.Blocked(netip.MustParseAddr(s)) {
			t.Errorf("%s: want blocked", s)
		}
	}

	allowed := []string{
		"8.8.8.8", "1.1.1.1", "93.184.216.34",
		"100.63.255.255", "100.128.0.1",
		"172.15.255.255", "172.32.0.1",
		"169.253.0.1", "192.0.1.1", "198.17.255.255", "198.20.0.1",
		"2606:4700:4700::1111", "2001:4860:4860::8888",
		"::ffff:8.8.8.8",
		// public embedded IPv4
		"64:ff9b::808:808", "2002:808:808::1",
	}
	for _, s := range allowed {
		if egress.Blocked(netip.MustParseAddr(s)) {
			t.Errorf("%s: want allowed", s)
		}
	}
}

func TestDialerBlocksLoopback(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = l.Close() })

	_, err = egress.NewDialer(false, time.Second).DialContext(context.Background(), "tcp", l.Addr().String())
	if !errors.Is(err, egress.ErrBlockedAddress) {
		t.Fatalf("err = %v, want ErrBlockedAddress", err)
	}
	// A hostname resolving to loopback is refused too: the check is on the resolved IP.
	_, err = egress.NewDialer(false, time.Second).DialContext(context.Background(), "tcp", net.JoinHostPort("localhost", "1"))
	if !errors.Is(err, egress.ErrBlockedAddress) {
		t.Fatalf("localhost: err = %v, want ErrBlockedAddress", err)
	}

	c, err := egress.NewDialer(true, time.Second).DialContext(context.Background(), "tcp", l.Addr().String())
	if err != nil {
		t.Fatalf("allowPrivate: %v", err)
	}
	_ = c.Close()
}
