# Feature list

> Status: living · Last verified: 2026-10-06 · [AI - claude-sonnet-5-5]

L7 egress proxy for mobile and backend API traffic: one stable entrypoint outside restrictive networks; upstream host chosen per request instead of a static ingress `externalName` per target.

## Implemented

- Env-based config, JSON logs, graceful shutdown.
- `GET /healthz`, `GET /readyz`, `GET /version` (unauthenticated, never proxied; JSON `{"version":"<build>"}`). The version is also logged once at startup (`starting`).
- L7 reverse proxy: `X-L7-Target-Host`, optional `X-L7-Target-Scheme` (default from `UPSTREAM_DEFAULT_SCHEME`).
- Upstream allow/deny via `UPSTREAM_ALLOW_HOSTS`, `UPSTREAM_ALLOW_SUFFIXES`, `UPSTREAM_DENY_HOSTS`.
- Auth optional, off by default, turned on by the tokens alone ([#36](https://github.com/denisitpro/lux-l7/issues/36)): `PROXY_AUTH_TOKEN` set means `instance` mode, `PROXY_AUTH_HOST_MAP` set means `per_host` mode, both set is a startup error. Clients send `X-L7-Proxy-Token`.
- Instance mode takes several tokens: `PROXY_AUTH_TOKEN=tok-a,tok-b,tok-c` (comma-separated, whitespace around items trimmed, empty items and duplicates dropped; a token cannot contain a comma). A request passes if its token matches any one entry; this holds for header mode and the CONNECT tunnel. Give each client its own token and revoke one by removing it and redeploying. Only SHA-256 hashes are kept; the presented token is hashed once and compared in constant time against every stored hash with no early exit. The token count (never the values) is logged at startup. `per_host` is unchanged.
  - Generate tokens with `openssl rand -hex 32` (64 hex chars).
  - Capacity: one env var value is capped by the kernel (Linux `MAX_ARG_STRLEN` = 128 KiB per env string) and a Kubernetes Secret by 1 MiB. With 64-char tokens plus a comma that is about 2000 tokens. Cost per request is one SHA-256 plus N 32-byte compares, negligible at that size.
- Routing headers stripped before the upstream request; in header mode a client `X-L7-Hops` is stripped too.
- Target validation (header mode, `400 bad_target`): no userinfo (`@`), non-empty host, numeric port `1..65535`. One trailing dot in the host is dropped (`localhost.` is `localhost`) before policy, auth and dial.
- Plain-http upstreams are off by default: `X-L7-Target-Scheme: http` is `400 bad_target` unless `UPSTREAM_ALLOW_HTTP=true`; `UPSTREAM_DEFAULT_SCHEME=http` without it fails at startup.
- Startup config errors: unparsable booleans (`UPSTREAM_ALLOW_HTTP=yes`), `UPSTREAM_MAX_BYTES`/`UPSTREAM_TIMEOUT`/`SHUTDOWN_TIMEOUT` <= 0, and port-bearing entries in the allow/deny lists (policy compares bare hostnames).
- Upstream failures: timeout `504` / `upstream_timeout`, other dial or transport errors `502` / `bad_gateway`; a client cancel gets no response and is logged at debug (both modes).
- Request paths are passed verbatim (`//a//b`, `/a/../b` are not cleaned or redirected); only `GET /healthz`, `/readyz`, `/version` are served locally.
- Single static binary, container image (`Dockerfile`), CI (`.github/workflows/ci.yml`: `go test -race`) and image release (`docker-release.yml`), Dependabot.
- SSRF guard (header mode, [#3](https://github.com/denisitpro/lux-l7/issues/3)): the resolved IP is checked at dial time (`internal/egress`, `net.Dialer.Control`), so names resolving or rebinding to internal addresses are refused. Blocked: unspecified, `0.0.0.0/8`, loopback, RFC 1918, CGNAT `100.64.0.0/10`, link-local incl. metadata `169.254.169.254`, `192.0.0.0/24`, benchmarking `198.18.0.0/15`, reserved `240.0.0.0/4`, multicast, IPv6 unique-local and link-local; IPv4-mapped IPv6 is checked as IPv4, NAT64 `64:ff9b::/96` and 6to4 `2002::/16` by their embedded IPv4. A blocked dial is `403 forbidden` "upstream address not allowed". The transport ignores `HTTP_PROXY` env so the guard sees the real target. `UPSTREAM_ALLOW_PRIVATE=true` turns it off (startup warning). Route mode is not guarded: its upstreams are ops-configured and may be local on purpose.
- Port allow-list (header mode): `UPSTREAM_ALLOW_PORTS` (default `80,443,8443,9443`) is checked on the target port (implicit: 443 https, 80 http); otherwise `403 forbidden` "upstream port not allowed". Invalid entries fail startup.
- **CONNECT tunnel** (`TUNNEL_ADDR`, [#23](https://github.com/denisitpro/lux-l7/issues/23)): separate listener that relays raw TCP after `CONNECT`, so TLS stays end to end and the proxy never sees API keys or signatures. See "CONNECT tunnel" below.
- **Route mode** (`PROXY_MODE=route`): static `ROUTES` table (host + path prefix → upstream origin). Client `X-L7-*` routing headers are ignored. Loop guard via `X-L7-Hops` / `ROUTE_MAX_HOPS`. `/healthz`, `/readyz`, `/version` stay local.

## Planned — core proxy

- Path-based routing `/h/<target-host>/<path>` for clients that only accept a base URL. GitHub: [#9](https://github.com/denisitpro/lux-l7/issues/9).
- WebSocket upgrade passthrough (likely works via std reverse proxy; needs explicit test).
- Server read/write timeouts break streaming, SSE and WebSocket; a chunked body over the limit returns 502 instead of 413. GitHub: [#6](https://github.com/denisitpro/lux-l7/issues/6).

## Routing protocol (client → proxy)

| Header | Required | Meaning |
|---|---|---|
| `X-L7-Target-Host` | yes | Upstream authority without scheme |
| `X-L7-Target-Scheme` | no | `https`, or `http` when `UPSTREAM_ALLOW_HTTP=true`; default `UPSTREAM_DEFAULT_SCHEME` |
| `X-L7-Proxy-Token` | when auth on | Instance or per-host token |

## Env (summary)

| Variable | Role |
|---|---|
| `UPSTREAM_ALLOW_HOSTS` / `UPSTREAM_ALLOW_SUFFIXES` / `UPSTREAM_DENY_HOSTS` | Upstream policy (header mode only) |
| `UPSTREAM_DEFAULT_SCHEME`, `UPSTREAM_TIMEOUT`, `UPSTREAM_MAX_BYTES` | Defaults and limits; timeout and max bytes must be > 0 |
| `UPSTREAM_ALLOW_HTTP` | Allow `http` upstreams in header mode (default `false`) |
| `UPSTREAM_ALLOW_PRIVATE` | Disable the SSRF guard in header mode (default `false`; local tests only) |
| `UPSTREAM_ALLOW_PORTS` | CSV of reachable target ports in header mode (default `80,443,8443,9443`) |
| `TUNNEL_ADDR` | CONNECT tunnel listen address; empty (default) disables it |
| `TUNNEL_ALLOW_PORTS` | CSV of reachable target ports on the tunnel (default `443`) |
| `TUNNEL_IDLE_TIMEOUT` | Per-tunnel idle limit, reset by traffic in either direction (default `10m`; must be > 0) |
| `TUNNEL_TLS_CERT_FILE` / `TUNNEL_TLS_KEY_FILE` | Client-to-proxy TLS pair, both or neither; reloaded on mtime change |
| `SHUTDOWN_TIMEOUT` | Graceful shutdown budget; must be > 0 |
| `PROXY_AUTH_TOKEN` / `PROXY_AUTH_HOST_MAP` | Auth (header mode only): off unless one of them is set; the choice also picks the mode |
| `PROXY_MODE` | `header` (default) or `route` |
| `ROUTES` | JSON route array (required when `PROXY_MODE=route`) |
| `ROUTE_MAX_HOPS` | Max `X-L7-Hops` before `508 loop_detected` (default `1`) |

Defaults: [`.env.example`](../.env.example).

### Minimal configs

Every variable is optional.

- **Zero env:** the service starts in header mode on `:8080`, auth off (a startup warning notes the empty allow-list).
- **Auth:** one variable, `PROXY_AUTH_TOKEN=<token>` (or `PROXY_AUTH_HOST_MAP=<json>` for per-host tokens).
- **CONNECT tunnel:** two variables, `TUNNEL_ADDR=:8443` and `PROXY_AUTH_TOKEN=<token>`. TLS cert files are recommended, not required. Without a token the tunnel needs `UPSTREAM_ALLOW_HOSTS` or `UPSTREAM_ALLOW_SUFFIXES`.

Legacy `AUTH_ENABLED` and `AUTH_MODE` are ignored except for safety checks, and a startup warning asks to remove them: `AUTH_ENABLED=true` without a token, `AUTH_ENABLED=false` with a token, and an `AUTH_MODE` that contradicts the token variable are startup errors. `AUTH_ENABLED=false` without tokens is valid, so existing deployments keep working. In route mode (no auth) tokens are ignored, and `AUTH_ENABLED=true` is an error.

### CONNECT tunnel

Header mode terminates TLS, so the proxy sees every header and body. The tunnel is the alternative for traffic that must stay opaque (exchange API keys and signatures): the client sends `CONNECT host:port`, the proxy dials the target and then copies bytes in both directions; the TLS session runs between the client and the target.

- **Exposure.** `TUNNEL_ADDR` runs its own `http.Server` that accepts only `CONNECT` (anything else is `405 method_not_allowed`). An L7 ingress cannot carry `CONNECT`: expose the port at L4 (a LoadBalancer Service, or ingress TLS passthrough). The header-mode listener is unchanged.
- **Check order.** Target syntax (`400 bad_target`: must be `host:port`, valid port, no userinfo; lowercased, one trailing dot dropped) → host policy (`UPSTREAM_ALLOW_*`, `UPSTREAM_DENY_HOSTS`; `403`) → `TUNNEL_ALLOW_PORTS` (`403`) → credentials (`407`) → dial.
- **Auth.** `Proxy-Authorization: Basic base64(user:token)`; the username is ignored, the token is checked by the same validator as header mode (`instance` or `per_host`, keyed by the CONNECT host). A failure is `407` with `Proxy-Authenticate: Basic realm="lux-l7"`. With auth off (no token set) no credentials are needed.
- **Ports.** `TUNNEL_ALLOW_PORTS` defaults to `443`. Exchange WebSocket streams also need `8443` and `9443` (OKX, Binance): add them explicitly, e.g. `TUNNEL_ALLOW_PORTS=443,8443,9443`.
- **SSRF guard.** The dial uses the same guarded dialer as header mode (`UPSTREAM_ALLOW_PRIVATE`, `UPSTREAM_TIMEOUT`): a blocked resolved IP is `403`, a dial timeout `504`, any other dial failure `502`. A `200 Connection Established` is sent only after the dial succeeds. Bytes the client pipelined behind the `CONNECT` (the TLS ClientHello) are forwarded to the target first.
- **Lifetime.** `TUNNEL_IDLE_TIMEOUT` is extended by traffic in either direction (a long download with a silent client stays open); there is no cap on total duration. When one side closes its sending half, the other side is half-closed and still drained. No read or write timeout is set on the tunnel server, only `HTTP_READ_HEADER_TIMEOUT` for the CONNECT request. Shutdown stops accepting, lets open tunnels run for up to `SHUTDOWN_TIMEOUT`, then force-closes them.
- **TLS to the proxy.** With `TUNNEL_TLS_CERT_FILE` + `TUNNEL_TLS_KEY_FILE` the listener speaks TLS 1.2+ with ALPN `http/1.1` only (an h2 `CONNECT` cannot be hijacked). The pair is re-read when either file's mtime changes (cert-manager rotation needs no restart); a failed reload keeps the old certificate and logs a warning. Without them the listener is plain HTTP and logs a startup warning: local tests only, credentials travel in clear text.
- **Startup errors.** `TUNNEL_ADDR` with `PROXY_MODE=route`; `TUNNEL_ADDR` with auth off (no token set) and no `UPSTREAM_ALLOW_HOSTS` / `UPSTREAM_ALLOW_SUFFIXES` (an open relay); only one of the TLS files; invalid `TUNNEL_ALLOW_PORTS` or `TUNNEL_IDLE_TIMEOUT`.
- **Logging.** Nothing per tunnel at `info`; the client address and `Proxy-Authorization` are never logged. Debug logs the target host only.
- **Not in v1.** Rate limits and a cap on concurrent tunnels.

Local check against a public site through a TLS tunnel listener:

```sh
openssl req -x509 -newkey ec -pkeyopt ec_paramgen_curve:prime256v1 -nodes -days 1 \
  -keyout tls.key -out tls.crt -subj /CN=localhost -addext "subjectAltName=IP:127.0.0.1"
TUNNEL_ADDR=127.0.0.1:8443 TUNNEL_TLS_CERT_FILE=tls.crt TUNNEL_TLS_KEY_FILE=tls.key \
UPSTREAM_ALLOW_SUFFIXES=example.com go run ./cmd/lux-l7
curl --proxy https://127.0.0.1:8443 --proxy-cacert tls.crt https://example.com/
```

### Route mode

Example `ROUTES` for regional relay (scenario A: `/location1` → core) plus local paths (scenario B: `/local` → local service, catch-all → core):

```json
[
  {"host":"example.com","path_prefix":"/local","upstream":"http://127.0.0.1:9001","rewrite_prefix":"/"},
  {"host":"example.com","path_prefix":"/location1","upstream":"https://origin.example.com:443","rewrite_prefix":"/"},
  {"host":"example.com","path_prefix":"/","upstream":"https://origin.example.com:443"}
]
```

`host` is exact or a suffix (`.example.com`). `path_prefix` matches on a segment boundary; `/` matches all paths. First match wins. Unmatched requests return `404` / `no_route`.

Host and SNI rule (upstreams are typically Cloudflare-fronted, which rejects SNI that differs from `Host`):

- The outgoing `Host` is fixed per route: `host_header` if set, else the route `host` when it is an exact name. An http route with a suffix `host` and no `host_header` passes the incoming `Host` through.
- For https upstreams the TLS SNI always equals the outgoing `Host` (without port), unless `tls_server_name` overrides it; an override that differs from the `Host` logs a startup warning.
- Each route has its own connection pool (never shared, even for the same `upstream`), so a connection is never reused for a different `Host`.
- An https route with a suffix `host` requires `host_header`; otherwise startup fails.
- Encoded paths are preserved: `%2F` is not decoded on the way to the upstream, with or without `rewrite_prefix`.
- `CONNECT` is rejected with `405` / `method_not_allowed`; the tunnel listener is header mode only.
- `X-Forwarded-Proto` is the proxy's own value (`https` when the client connection used TLS, else `http`); a client-sent value is ignored.
- Auth is not supported in route mode: `AUTH_ENABLED=true` fails at startup. `UPSTREAM_ALLOW_HOSTS`, `UPSTREAM_ALLOW_SUFFIXES` and `UPSTREAM_DENY_HOSTS` are ignored (startup warning).

## Planned — cloud native

- Config entirely from env (12-factor); no reload required for allow-list changes in v1 (restart pod).
- Horizontal scale: stateless replicas behind a Service; no sticky sessions.
- Optional `POD_IP` / standard downward API for logs only.
- Deploy: plain image through a generic Helm chart; no repo-owned manifests for now.

## Planned — security

- Refuse to start with an empty allow-list unless `UPSTREAM_ALLOW_ANY=true` (client tokens are extractable; the allow-list is the real abuse guard). GitHub: [#7](https://github.com/denisitpro/lux-l7/issues/7).
- Optional client IP forwarding (`X-Forwarded-For`) with trusted-hop handling. GitHub: [#8](https://github.com/denisitpro/lux-l7/issues/8).

## Planned — observability

GitHub: [#1](https://github.com/denisitpro/lux-l7/issues/1) — structured logs, request IDs, Prometheus `/metrics`, document env in this file when done.

## Explicit non-goals (v1)

- MITM TLS or custom CA on clients (clients speak TLS to the proxy; proxy speaks TLS to upstream).
- DNS or general L4 tunneling (the CONNECT tunnel relays TCP to allow-listed hosts and ports only).
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
