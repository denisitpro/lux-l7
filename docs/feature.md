# Feature list

> Status: living · Last verified: 2026-10-05 · [AI - claude-sonnet-5-5]

L7 egress proxy for mobile and backend API traffic: one stable entrypoint outside restrictive networks; upstream host chosen per request instead of a static ingress `externalName` per target.

## Implemented

- Env-based config, JSON logs, graceful shutdown.
- `GET /healthz`, `GET /readyz`.
- L7 reverse proxy: `X-L7-Target-Host`, optional `X-L7-Target-Scheme` (default from `UPSTREAM_DEFAULT_SCHEME`).
- Upstream allow/deny via `UPSTREAM_ALLOW_HOSTS`, `UPSTREAM_ALLOW_SUFFIXES`, `UPSTREAM_DENY_HOSTS`.
- Auth optional (`AUTH_ENABLED=false` by default): `X-L7-Proxy-Token`, modes `instance` / `per_host`.
- Routing headers stripped before the upstream request.
- Auth **disabled by default** for local and integration tests.
- Single static binary, container image (`Dockerfile`), CI (`.github/workflows/ci.yml`: `go test -race`) and image release (`docker-release.yml`), Dependabot.

## Planned — core proxy

- Path-based routing `/h/<target-host>/<path>` for clients that only accept a base URL. GitHub: [#9](https://github.com/denisitpro/lux-l7/issues/9).
- WebSocket upgrade passthrough (likely works via std reverse proxy; needs explicit test).
- Server read/write timeouts break streaming, SSE and WebSocket; a chunked body over the limit returns 502 instead of 413. GitHub: [#6](https://github.com/denisitpro/lux-l7/issues/6).

## Routing protocol (client → proxy)

| Header | Required | Meaning |
|---|---|---|
| `X-L7-Target-Host` | yes | Upstream authority without scheme |
| `X-L7-Target-Scheme` | no | `http` or `https`; default `UPSTREAM_DEFAULT_SCHEME` |
| `X-L7-Proxy-Token` | when auth on | Instance or per-host token |

## Env (summary)

| Variable | Role |
|---|---|
| `UPSTREAM_ALLOW_HOSTS` / `UPSTREAM_ALLOW_SUFFIXES` / `UPSTREAM_DENY_HOSTS` | Upstream policy |
| `UPSTREAM_DEFAULT_SCHEME`, `UPSTREAM_TIMEOUT`, `UPSTREAM_MAX_BYTES` | Defaults and limits |
| `AUTH_ENABLED`, `AUTH_MODE`, `PROXY_AUTH_TOKEN`, `PROXY_AUTH_HOST_MAP` | Auth |

Defaults: [`.env.example`](../.env.example).

## Planned — cloud native

- Config entirely from env (12-factor); no reload required for allow-list changes in v1 (restart pod).
- Horizontal scale: stateless replicas behind a Service; no sticky sessions.
- Optional `POD_IP` / standard downward API for logs only.
- Deploy: plain image through a generic Helm chart; no repo-owned manifests for now.

## Planned — security

GitHub: [#3](https://github.com/denisitpro/lux-l7/issues/3) — SSRF guard: resolved-IP check at dial time blocks loopback, private, link-local and metadata addresses; port allow-list; trailing-dot host normalisation.

- Refuse to start with an empty allow-list unless `UPSTREAM_ALLOW_ANY=true` (client tokens are extractable; the allow-list is the real abuse guard). GitHub: [#7](https://github.com/denisitpro/lux-l7/issues/7).
- Optional client IP forwarding (`X-Forwarded-For`) with trusted-hop handling. GitHub: [#8](https://github.com/denisitpro/lux-l7/issues/8).

## Planned — observability

GitHub: [#1](https://github.com/denisitpro/lux-l7/issues/1) — structured logs, request IDs, Prometheus `/metrics`, document env in this file when done.

## Explicit non-goals (v1)

- MITM TLS or custom CA on clients (clients speak TLS to the proxy; proxy speaks TLS to upstream).
- DNS or L4 tunneling.
- Per-tenant billing or quota (may come later).

## Prior art (why custom service)

| Project | Relevance |
|---|---|
| [Caddy `reverse_proxy` dynamic upstream](https://caddyserver.com/docs/caddyfile/directives/reverse_proxy) | Placeholder upstream addresses; good for ops-owned config, awkward for per-request mobile routing + token model without a custom module. |
| [caddy-redis-router](https://github.com/n0isy/caddy-redis-router) | Data-driven upstream in Redis; adds moving parts vs env + headers. |
| [Dyn-Proxy-Go](https://github.com/hmdmph/Dyn-Proxy-Go) | Host-based dynamic reverse proxy + k8s; closest OSS shape; lacks our auth and header contract. |
| [g3proxy](https://github.com/bytedance/g3) | Enterprise forward/reverse proxy; heavy for a focused egress helper. |
| [Envoy dynamic forward proxy](https://www.envoyproxy.io/docs/envoy/latest/configuration/http/http_filters/dynamic_forward_proxy_filter) | DNS-cached dynamic upstream from the Host header, closest off-the-shelf equivalent; per-request target from a custom header plus token auth means Lua/ext_authz glue and a large config surface. |
| nginx `proxy_pass https://$http_<header>` with `resolver` | Works for a handful of hosts; no constant-time token compare, SSRF guard or per-host token model without Lua/njs. |
| [Pingora](https://github.com/cloudflare/pingora) (Rust framework from Cloudflare) | High-performance Rust proxy framework; a library, not a product: same custom code, higher dev cost. |
| [Vendor SDK "proxy URL" setting](https://documentation.qonversion.io/docs/solving-russian-federation-traffic-blocking) | Third-party SDKs already ship a configurable proxy base URL for the same regional CDN blocking; confirms the client-side switch pattern. |

Custom Go service keeps the mobile contract, allow-list envs, and auth in one small codebase aligned with other personal Go repos.

## Language: Go (decision)

| | Go | Rust (hyper / Pingora) |
|---|---|---|
| Proxy core | `net/http` `httputil.ReverseProxy` covers HTTP/1.1, HTTP/2, WebSocket upgrade, trailers out of the box | More code for the same features |
| Iteration | Fast; one static binary | Slower; long compile times |
| Runtime | GC adds small tail-latency jitter and higher RSS per connection | No GC; lowest p99 and memory per connection; best for very high RPS (tens of thousands per core) |
| Ecosystem | Matches other personal Go services (shared Makefile/CI/Dockerfile) | Smaller pool of copy-able sibling tooling |

Latency is dominated by the client→proxy→CDN round trips (tens of ms), not by proxy CPU (sub-ms per request in either language). Go is chosen; revisit Rust/Pingora only if profiling under real load shows the proxy itself as the bottleneck.
