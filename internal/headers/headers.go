// Package headers names the L7 routing and auth headers (client → proxy).
package headers

const (
	TargetHost   = "X-L7-Target-Host"
	TargetScheme = "X-L7-Target-Scheme"
	ProxyToken   = "X-L7-Proxy-Token"
	Hops         = "X-L7-Hops"
)
