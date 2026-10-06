package config

import (
	"net"
	"strconv"
)

// sameListenAddr reports whether two listen addresses would fight for one socket:
// the ports are equal and the hosts are equal or either one is a wildcard (empty,
// "0.0.0.0" or "::"), so ":8080", "0.0.0.0:8080" and "[::]:8080" all clash, and so
// does ":8080" with "127.0.0.1:8080". Hostnames are not resolved ("localhost" and
// "127.0.0.1" count as different). An address that does not parse as host:port, or
// with port 0 (the OS picks a free port), never clashes: the listener reports it.
func sameListenAddr(a, b string) bool {
	ha, pa, okA := splitListenAddr(a)
	hb, pb, okB := splitListenAddr(b)
	if !okA || !okB || pa != pb || pa == 0 {
		return false
	}
	return ha == "" || hb == "" || ha == hb
}

func splitListenAddr(addr string) (host string, port uint64, ok bool) {
	h, p, err := net.SplitHostPort(addr)
	if err != nil {
		return "", 0, false
	}
	n, err := strconv.ParseUint(p, 10, 16)
	if err != nil {
		return "", 0, false
	}
	if ip := net.ParseIP(h); ip != nil {
		if ip.IsUnspecified() {
			h = ""
		} else {
			h = ip.String()
		}
	}
	return h, n, true
}
