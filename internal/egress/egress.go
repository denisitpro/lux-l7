// Package egress guards outbound dials against SSRF: the check runs on the resolved
// IP at connect time, so a hostname that resolves (or rebinds) to an internal
// address is refused just like a literal one. It is independent of HTTP so any
// outbound path (plain requests, CONNECT tunnels) can reuse the same dialer.
package egress

import (
	"errors"
	"fmt"
	"net"
	"net/netip"
	"syscall"
	"time"
)

// ErrBlockedAddress marks a dial refused because the resolved IP is not public.
var ErrBlockedAddress = errors.New("egress: address not allowed")

var blockedPrefixes = mustPrefixes(
	"0.0.0.0/8",      // "this" network, incl. unspecified
	"10.0.0.0/8",     // RFC 1918
	"100.64.0.0/10",  // CGNAT
	"127.0.0.0/8",    // loopback
	"169.254.0.0/16", // link-local, incl. cloud metadata 169.254.169.254
	"172.16.0.0/12",  // RFC 1918
	"192.0.0.0/24",   // IETF protocol assignments
	"192.168.0.0/16", // RFC 1918
	"198.18.0.0/15",  // benchmarking
	"224.0.0.0/4",    // multicast
	"240.0.0.0/4",    // reserved, incl. 255.255.255.255
	"::/128",         // unspecified
	"::1/128",        // loopback
	"64:ff9b:1::/48", // local-use NAT64 (RFC 8215)
	"fc00::/7",       // unique-local
	"fe80::/10",      // link-local
	"ff00::/8",       // multicast
)

var (
	nat64Prefix = netip.MustParsePrefix("64:ff9b::/96")
	sixToFour   = netip.MustParsePrefix("2002::/16")
	// Deprecated IPv4-compatible IPv6 (RFC 4291); `::` and `::1` fall in it too and
	// stay blocked because their embedded IPv4 is in 0.0.0.0/8.
	v4Compatible = netip.MustParsePrefix("::/96")
)

func mustPrefixes(cidrs ...string) []netip.Prefix {
	out := make([]netip.Prefix, len(cidrs))
	for i, c := range cidrs {
		out[i] = netip.MustParsePrefix(c)
	}
	return out
}

// Blocked reports whether ip is not a public unicast address. IPv4-mapped IPv6 is
// checked as IPv4; NAT64, 6to4 and IPv4-compatible (`::/96`) addresses are checked by their embedded IPv4.
func Blocked(ip netip.Addr) bool {
	ip = ip.WithZone("").Unmap()
	if !ip.IsValid() {
		return true
	}
	if ip.Is6() {
		b := ip.As16()
		switch {
		case nat64Prefix.Contains(ip):
			return Blocked(netip.AddrFrom4([4]byte{b[12], b[13], b[14], b[15]}))
		case sixToFour.Contains(ip):
			return Blocked(netip.AddrFrom4([4]byte{b[2], b[3], b[4], b[5]}))
		case v4Compatible.Contains(ip):
			return Blocked(netip.AddrFrom4([4]byte{b[12], b[13], b[14], b[15]}))
		}
	}
	for _, p := range blockedPrefixes {
		if p.Contains(ip) {
			return true
		}
	}
	return false
}

// control rejects a non-public destination. address is the resolved ip:port.
func control(_, address string, _ syscall.RawConn) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return fmt.Errorf("%w: %q", ErrBlockedAddress, address)
	}
	ip, err := netip.ParseAddr(host)
	if err != nil || Blocked(ip) {
		return fmt.Errorf("%w: %s", ErrBlockedAddress, host)
	}
	return nil
}

// MaxIdleConnsPerHost is the per-upstream idle connection pool size shared by the
// header, path and route mode transports (net/http defaults to 2, which churns
// connections when traffic concentrates on a few upstreams).
const MaxIdleConnsPerHost = 64

// NewDialer returns a dialer that refuses non-public destinations after DNS
// resolution. With allowPrivate it is a plain dialer (local tests, trusted setups).
func NewDialer(allowPrivate bool, timeout time.Duration) *net.Dialer {
	d := &net.Dialer{Timeout: timeout, KeepAlive: 30 * time.Second}
	if !allowPrivate {
		d.Control = control
	}
	return d
}
