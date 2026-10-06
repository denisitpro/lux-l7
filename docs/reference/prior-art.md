# Prior art and the Go decision

> Status: living · Last verified: 2026-10-06 · [AI - claude-sonnet-5-5]

Why a custom service exists and why it is written in Go. The decision stands until profiling under real load shows the proxy itself as the bottleneck.

## Prior art

| Project | Relevance |
|---|---|
| [Caddy `reverse_proxy` dynamic upstream](https://caddyserver.com/docs/caddyfile/directives/reverse_proxy) | Placeholder upstream addresses; good for ops-owned config, awkward for per-request routing plus a token model without a custom module. |
| [caddy-redis-router](https://github.com/n0isy/caddy-redis-router) | Data-driven upstream in Redis; adds moving parts compared with env plus headers. |
| [Dyn-Proxy-Go](https://github.com/hmdmph/Dyn-Proxy-Go) | Host-based dynamic reverse proxy for Kubernetes; closest open-source shape; lacks the auth and header contract. |
| [g3proxy](https://github.com/bytedance/g3) | Enterprise forward and reverse proxy; heavy for a focused egress helper. |
| [Envoy dynamic forward proxy](https://www.envoyproxy.io/docs/envoy/latest/configuration/http/http_filters/dynamic_forward_proxy_filter) | DNS-cached dynamic upstream from the Host header, the closest off-the-shelf equivalent; a per-request target from a custom header plus token auth means Lua or ext_authz glue and a large config surface. |
| nginx `proxy_pass https://$http_<header>` with `resolver` | Works for a handful of hosts; no constant-time token compare, SSRF guard or per-host token model without Lua or njs. |
| [Pingora](https://github.com/cloudflare/pingora) | High-performance Rust proxy framework; a library, not a product: the same custom code at higher development cost. |
| Vendor SDK "proxy URL" setting | Third-party SDKs ship a configurable proxy base URL for regional CDN blocking; confirms the client-side switch pattern. |

A custom Go service keeps the client contract, allow-list env and auth in one small codebase aligned with sibling Go services.

## Language: Go

| | Go | Rust (hyper / Pingora) |
|---|---|---|
| Proxy core | `net/http` `httputil.ReverseProxy` covers HTTP/1.1, HTTP/2 to upstream and upgrade handling out of the box | More code for the same features |
| Iteration | Fast; one static binary | Slower; long compile times |
| Runtime | GC adds small tail-latency jitter and higher memory per connection | No GC; lowest p99 and memory per connection; best for very high request rates |
| Ecosystem | Matches sibling Go services (shared Makefile, CI, Dockerfile) | Smaller pool of copy-able sibling tooling |

Latency is dominated by the client, proxy and upstream round trips (tens of ms), not by proxy CPU (sub-millisecond per request in either language).
