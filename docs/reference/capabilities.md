# Capabilities

> Status: living · Last verified: 2026-10-07 · [AI - claude-sonnet-5-5]

What lux-l7 does, verified against `cmd/` and `internal/`. One entry per capability: what it does, how to enable it, limits, origin. Wire details live in [`protocol.md`](protocol.md), every env variable in [`configuration.md`](configuration.md). The roadmap is the [`label:feature` issue list](https://github.com/denisitpro/lux-l7/issues?q=is%3Aopen+label%3Afeature).

lux-l7 is an L7 egress proxy: one stable entrypoint outside restrictive networks, the upstream chosen per request instead of a static ingress target per host. A PR that adds or changes a capability updates this file.

Origin links point at the merged PR. Capabilities from the first PR have no finer origin.

## Modes

One process runs in one mode (`PROXY_MODE`); the tunnel is an extra listener of header and path mode.

### Header mode

- **What:** forwards a request to the upstream named by `X-L7-Target-Host` (optional `X-L7-Target-Scheme`). Request path and query pass through verbatim, the outgoing `Host` is the target authority.
- **Enable:** default (`PROXY_MODE=header`), no other variable required.
- **Limits:** request checks run in a fixed order, see [`protocol.md`](protocol.md). Upstream response codes and bodies are passed through unchanged. `X-Forwarded-*` headers are not sent to the upstream by default (client-sent ones are dropped too). `UPSTREAM_FORWARD_CLIENT_IP=true` sends `X-Forwarded-For` (inbound chain, trusted by default, plus the connecting address), `X-Forwarded-Host` and `X-Forwarded-Proto`; `UPSTREAM_TRUSTED_PROXIES` (IPs/CIDRs, IPv4 and IPv6) limits whose inbound values are honoured, see [`configuration/upstream-policy.md`](configuration/upstream-policy.md).
- **Origin:** [#2](https://github.com/denisitpro/lux-l7/pull/2), hardened in [#30](https://github.com/denisitpro/lux-l7/pull/30); client IP forwarding [#8](https://github.com/denisitpro/lux-l7/issues/8).

### Path mode

- **What:** the target is the first path segment: `/h/<host>[:port]/<path>?<query>` is forwarded to `<scheme>://<host>[:port]/<path>?<query>`, for clients that cannot set headers (a browser, a tool that only takes a base URL). The scheme is always `UPSTREAM_DEFAULT_SCHEME`; `X-L7-Target-Host` and `X-L7-Target-Scheme` are ignored and stripped. An empty path (`/h/api.example.com`) is `/`; path and query pass verbatim, `%2F` included; an IPv6 literal is bracketed (`/h/[2001:db8::1]:443/x`). `Location` and `Content-Location` response headers that point at the target (absolute URL with the same scheme and host[:port], or an absolute path) are rewritten to `/h/<host>/...`, so a redirect stays behind the proxy; other hosts and relative values are untouched.
- **Enable:** `PROXY_MODE=path`. Auth, host policy, SSRF guard, port allow-list, body limit, error codes and request log are the header-mode ones, in the same order ([`protocol.md`](protocol.md)); only the target source differs. A bad target is `400 bad_target`.
- **Limits:** the token stays in `X-L7-Proxy-Token`, never in path or query (it would reach access logs); a client that cannot set headers runs with auth off and relies on the allow list. `Set-Cookie` is not rewritten, so cookies set by the target apply to the proxy origin ([#51](https://github.com/denisitpro/lux-l7/issues/51)); HTML bodies are not rewritten, so absolute links inside pages bypass the proxy. A percent-encoded host segment is refused. `ROUTES` must not be set. The tunnel and `X-Forwarded-*` (`UPSTREAM_FORWARD_CLIENT_IP`, `UPSTREAM_TRUSTED_PROXIES`) work as in header mode. Metrics and the request log label it `mode="path"`.
- **Origin:** [#9](https://github.com/denisitpro/lux-l7/issues/9).

### CONNECT tunnel

- **What:** a second listener that accepts only `CONNECT host:port`, dials the target and relays raw TCP both ways. TLS runs between the client and the target, so the proxy never sees headers, bodies, API keys or signatures; it sees only the target host:port.
- **Enable:** `TUNNEL_ADDR` plus a token (`PROXY_AUTH_TOKEN`) or an allow-list (`UPSTREAM_ALLOW_HOSTS` / `UPSTREAM_ALLOW_SUFFIXES`). Startup fails without either, and with `PROXY_MODE=route` (header and path mode both work).
- **Limits:** needs an L4 exposure (LoadBalancer Service or TLS passthrough); an L7 ingress cannot carry `CONNECT`. Target ports come from `TUNNEL_ALLOW_PORTS` (default `443`; exchange WebSocket ports 8443 and 9443 must be added explicitly). Credentials are `Proxy-Authorization: Basic base64(user:token)`; the user part is ignored. A refused `CONNECT` (407, 400, 403, 405, 5xx) answers with `Connection: close` and the connection is closed; `HTTP_IDLE_TIMEOUT` also bounds any keep-alive wait on the tunnel listener ([#57](https://github.com/denisitpro/lux-l7/issues/57)). No rate limit and no cap on concurrent tunnels exist in the code. Host policy, the SSRF guard and the token validator are shared with header and path mode. Idle, shutdown and TLS behaviour: see the Operations entries below and [`protocol.md`](protocol.md).
- **Origin:** [#32](https://github.com/denisitpro/lux-l7/pull/32) (issue [#23](https://github.com/denisitpro/lux-l7/issues/23)).

### Route mode

- **What:** a static table (`ROUTES`) maps incoming `Host` + path prefix to an upstream origin, with optional path-prefix rewrite. Clients send ordinary requests; no routing headers are read (only `X-L7-Hops`, the loop counter). First matching route wins, no match is `404 no_route`. `path_prefix` matches on a segment boundary and a trailing `/` is dropped at load (`/api/` is `/api`, [#58](https://github.com/denisitpro/lux-l7/issues/58)).
- **Enable:** `PROXY_MODE=route` and `ROUTES` (JSON array). Route fields are in [`configuration/routes.md`](configuration/routes.md).
- **Limits:** no auth (a token variable fails startup), no tunnel, no host policy, no SSRF guard and no port allow-list: upstreams are operator-configured. `UPSTREAM_ALLOW_HOSTS`, `UPSTREAM_ALLOW_SUFFIXES` and `UPSTREAM_DENY_HOSTS` are ignored with a startup warning. `CONNECT` is `405 method_not_allowed`. Each route has its own connection pool and transport, which ignores `HTTP_PROXY` / `HTTPS_PROXY` / `NO_PROXY` (upstreams are always dialled directly, so the per-route SNI = Host guarantee holds). Outgoing `Host` is fixed per route: `host_header`, else the `upstream` authority as written (host plus port, e.g. `origin.example.com:8443`), regardless of the route `host`. Breaking change (#43): earlier versions defaulted to the route `host`; configs that relied on that must now set `host_header` to the route host. For https upstreams the TLS SNI equals the outgoing `Host` unless `tls_server_name` overrides it (a differing override logs a startup warning); the SNI never carries a port. Encoded paths (`%2F`) are preserved. The proxy sets `X-Forwarded-For` (connecting client address), `X-Forwarded-Host` (incoming `Host`) and `X-Forwarded-Proto` (`https` only when the client connection used TLS), ignoring client-sent values.
- **Origin:** [#25](https://github.com/denisitpro/lux-l7/pull/25); `X-Forwarded-Proto` rule in [#30](https://github.com/denisitpro/lux-l7/pull/30).

## Security

### Auth tokens

- **What:** clients prove access with a token: `X-L7-Proxy-Token` in header and path mode, the Basic password in the tunnel. Two models. `instance`: one or several shared tokens (`PROXY_AUTH_TOKEN=tok-a,tok-b`, comma-separated, whitespace trimmed, empty items and duplicates dropped); a request passes if its token matches any entry. `per_host`: a JSON map host to token (`PROXY_AUTH_HOST_MAP`), keyed by the bare target host (port ignored, lowercased, exact match, no suffixes).
- **Enable:** auth is off unless a token variable is set; the variable chosen picks the model. Both set is a startup error. There is no other switch: the removed `AUTH_ENABLED` / `AUTH_MODE` are ignored with a startup warning that states the actual auth state.
- **Limits:** header mode, path mode and tunnel; route mode has no auth. Only SHA-256 hashes are kept; the presented token is hashed once and compared in constant time against every stored hash with no early exit. Startup logs the token count, never values. A token cannot contain a comma. Capacity is bounded by the 128 KiB single-env-string limit on Linux and 1 MiB per Kubernetes Secret (about two thousand 64-character tokens). Order: the token is checked before host policy and port (`401` in header and path mode, `407` in the tunnel), so an unauthenticated client cannot probe the allow list or ports through `403`. With auth off, startup logs a warning not to expose it publicly. Revoking a token means removing it and redeploying.
- **Origin:** [#2](https://github.com/denisitpro/lux-l7/pull/2); multiple tokens [#34](https://github.com/denisitpro/lux-l7/pull/34); inferred from tokens [#38](https://github.com/denisitpro/lux-l7/pull/38); token-first order [#40](https://github.com/denisitpro/lux-l7/issues/40); `AUTH_ENABLED` / `AUTH_MODE` removed [#41](https://github.com/denisitpro/lux-l7/issues/41).

### Upstream policy

- **What:** allow and deny lists on the target hostname. Deny wins. A non-empty allow list (hosts or suffixes) admits only matching names; with both allow lists empty every host is admitted unless denied. A suffix entry matches the apex and all subdomains (`example.com` and `.example.com` behave the same). Deny entries are exact hosts.
- **Enable:** `UPSTREAM_ALLOW_HOSTS`, `UPSTREAM_ALLOW_SUFFIXES`, `UPSTREAM_DENY_HOSTS` (all optional).
- **Limits:** header mode, path mode and tunnel. Bare hostnames only: an entry with a port fails startup. An empty allow list logs a startup warning in header and path mode. One trailing dot is dropped from target and entries, so `localhost.` cannot bypass a deny of `localhost`.
- **Origin:** [#2](https://github.com/denisitpro/lux-l7/pull/2); trailing dot and port-entry rules [#30](https://github.com/denisitpro/lux-l7/pull/30).

### SSRF guard

- **What:** the resolved IP is checked at dial time (`net.Dialer.Control`), so a name that resolves or rebinds to an internal address is refused as a literal one would be. Blocked: unspecified and `0.0.0.0/8`, loopback, RFC 1918, CGNAT, link-local (including the metadata address), `192.0.0.0/24`, benchmarking, reserved `240.0.0.0/4`, multicast, IPv6 unique-local, link-local and local-use NAT64 `64:ff9b:1::/48`. IPv4-mapped IPv6 is checked as IPv4; `64:ff9b::/96` (NAT64), `2002::/16` (6to4) and the deprecated IPv4-compatible `::/96` are judged by the embedded IPv4 ([#60](https://github.com/denisitpro/lux-l7/issues/60)). A blocked dial is `403 forbidden` "upstream address not allowed".
- **Enable:** always on in header mode, path mode and the tunnel. `UPSTREAM_ALLOW_PRIVATE=true` turns it off (startup warning; local tests only).
- **Limits:** not applied in route mode. The header- and path-mode transport ignores `HTTP_PROXY` so the guard sees the real target.
- **Origin:** [#31](https://github.com/denisitpro/lux-l7/pull/31) (issue [#3](https://github.com/denisitpro/lux-l7/issues/3)).

### Port allow-list

- **What:** limits the target ports a client may reach. Header mode checks the port in `X-L7-Target-Host` (path mode: in the path segment), implicit 443 for https and 80 for http. The tunnel checks the `CONNECT` port.
- **Enable:** always on. `UPSTREAM_ALLOW_PORTS` (default `80,443,8443,9443`) for header and path mode, `TUNNEL_ALLOW_PORTS` (default `443`) for the tunnel. Refusal is `403 forbidden` "upstream port not allowed".
- **Limits:** invalid entries fail startup. Not applied in route mode.
- **Origin:** [#31](https://github.com/denisitpro/lux-l7/pull/31).

### Scheme gate

- **What:** `X-L7-Target-Scheme: http`, or `UPSTREAM_DEFAULT_SCHEME=http`, would let a client downgrade the upstream hop. Plain http is refused unless enabled.
- **Enable:** off by default; `UPSTREAM_ALLOW_HTTP=true` enables it. A disallowed scheme header is `400 bad_target`; `UPSTREAM_DEFAULT_SCHEME=http` without the allowance fails startup.
- **Limits:** the header is header mode only; path mode has no client scheme override and uses `UPSTREAM_DEFAULT_SCHEME`, so `http` there needs both `UPSTREAM_DEFAULT_SCHEME=http` and the allowance. Route upstream schemes come from `ROUTES`.
- **Origin:** [#30](https://github.com/denisitpro/lux-l7/pull/30).

### Header stripping

- **What:** `X-L7-Target-Host`, `X-L7-Target-Scheme` and `X-L7-Proxy-Token` never reach the upstream (header and path mode; route mode removes the same three). In header mode a client `X-L7-Hops` is removed too.
- **Enable:** always on.
- **Limits:** in route mode `X-L7-Hops` is the loop counter and is forwarded, incremented.
- **Origin:** [#2](https://github.com/denisitpro/lux-l7/pull/2); hops [#30](https://github.com/denisitpro/lux-l7/pull/30).

### Loop guard

- **What:** route mode increments `X-L7-Hops` on each hop and refuses a request whose incoming count is at or above the limit (`508 loop_detected`). A non-integer or negative value is `400 bad_hops`.
- **Enable:** always on in route mode; limit `ROUTE_MAX_HOPS` (default `1`, must be at least 1).
- **Limits:** route mode only. Header mode has no loop guard.
- **Origin:** [#25](https://github.com/denisitpro/lux-l7/pull/25).

### Target validation and body limit

- **What:** the header-mode target (and the path-mode host segment) must be a non-empty host with an optional port `1..65535` and no userinfo, path, query or fragment characters (`400 bad_target`). Request bodies above `UPSTREAM_MAX_BYTES` are refused: `413 body_too_large` when `Content-Length` says so up front, otherwise (chunked) the body is cut while streaming and the proxy answers `413 body_too_large` too.
- **Enable:** always on; `UPSTREAM_MAX_BYTES` (default 32 MiB, must be > 0).
- **Limits:** the body limit applies to every mode.
- **Origin:** [#2](https://github.com/denisitpro/lux-l7/pull/2); validation [#30](https://github.com/denisitpro/lux-l7/pull/30).

## Protocol support

- **HTTP/1.1 from clients:** the main listener has no TLS configuration and no h2c, so it serves HTTP/1.x; TLS is expected to end at an ingress. The tunnel TLS listener offers ALPN `http/1.1` only.
- **HTTP to upstream:** both modes use a transport with `ForceAttemptHTTP2: true`, so an https upstream may be spoken to over HTTP/2 when it negotiates `h2`; the code does nothing more. TLS to upstream is 1.2 or higher.
- **WebSocket and streaming through header or route mode:** SSE, long downloads, slow uploads and WebSocket (`Upgrade`, then bytes both ways) pass through `httputil.ReverseProxy`; tests cover SSE and a raw WebSocket handshake in both modes. `HTTP_READ_TIMEOUT` and `HTTP_WRITE_TIMEOUT` default to `0` (off); the protection is `HTTP_READ_HEADER_TIMEOUT`, `HTTP_IDLE_TIMEOUT` and the upstream response-header wait (`UPSTREAM_TIMEOUT`). Setting a non-zero read or write timeout caps the whole request or response and cuts streams again ([#6](https://github.com/denisitpro/lux-l7/issues/6)). The CONNECT tunnel remains an option for raw long-lived connections (add ports 8443 and 9443 to `TUNNEL_ALLOW_PORTS`).
- **Upstream errors:** an upstream timeout is `504 upstream_timeout`; another dial or transport error `502 bad_gateway`; a client cancel gets no response (it shows only in the `debug` request line). Same in both modes and in the tunnel dial.
- **Paths:** passed verbatim, no cleaning or redirects (`//a//b`, `/a/../b`).
- **Origin:** [#2](https://github.com/denisitpro/lux-l7/pull/2); error mapping and verbatim paths [#30](https://github.com/denisitpro/lux-l7/pull/30).

## Operations

### Zero-config start

- **What:** no environment variable is required. With none set the service starts in header mode on `:8080`, auth off, with startup warnings (empty allow list, auth disabled).
- **Enable:** always on. An optional `test.env` in the working directory is loaded without overriding existing variables.
- **Limits:** that default is open (any public host, ports 80/443/8443/9443); do not expose it unauthenticated. A planned startup refusal is [#7](https://github.com/denisitpro/lux-l7/issues/7).
- **Origin:** [#38](https://github.com/denisitpro/lux-l7/pull/38) (issue [#36](https://github.com/denisitpro/lux-l7/issues/36)).

### Probes and /version

- **What:** `GET /healthz` and `/readyz` answer `200 ok`; `GET /version` answers `{"version":"<build id>"}`. All three are served locally, need no auth and are never proxied, in both modes. The build identity is also logged once at startup.
- **Enable:** always on.
- **Limits:** `GET` only (`HEAD /healthz` is proxied). `/readyz` does not check upstreams. `/version` returns the build id only.
- **Origin:** probes [#2](https://github.com/denisitpro/lux-l7/pull/2); `/version` [#19](https://github.com/denisitpro/lux-l7/pull/19); GET-only rule [#30](https://github.com/denisitpro/lux-l7/pull/30).

### Proxy-origin marker

- **What:** every proxy-generated error response (header, route and tunnel mode) carries `X-L7-Proxy: 1`, so a client can tell a proxy `403`/`502` from an upstream one. A marker sent by an upstream is removed from the relayed response; upstream responses never carry it.
- **Enable:** always on, no setting.
- **Limits:** not set on the probes (`/healthz`, `/readyz`, `/version`) or on the `200 Connection Established` that opens a tunnel. A client cancel gets no response, so no marker.
- **Origin:** issue [#13](https://github.com/denisitpro/lux-l7/issues/13).

### Graceful shutdown

- **What:** on SIGINT or SIGTERM the main server and the tunnel stop accepting and drain for up to `SHUTDOWN_TIMEOUT`; open tunnels still alive at the deadline are force-closed. A failed shutdown exits non-zero.
- **Enable:** always on; `SHUTDOWN_TIMEOUT` (default 15s, must be > 0).
- **Limits:** a tunnel arriving during shutdown gets `503 shutting_down`.
- **Origin:** [#2](https://github.com/denisitpro/lux-l7/pull/2); tunnel part [#32](https://github.com/denisitpro/lux-l7/pull/32).

### JSON logs

- **What:** structured JSON on stdout (`log/slog`); level from `LOG_LEVEL`. There is no other logging variable: the proxy mode decides what each level emits. Config warnings are logged after the handler is installed. Logging works the same with `METRICS_ADDR` on or off.
- **Enable:** always on; `LOG_LEVEL` (`debug`, `info`, `warn`, `error`; default `info`).
- **Header mode and tunnel** (the log must not leak): at `info` nothing per request, and never the client address, path, query, target host or a token. At `warn` and `error` an upstream failure is logged as its reason only (`timeout`, `blocked` at `warn`; any other transport fault at `error`), deduplicated: the first occurrence at once, then one `upstream errors repeated` line with a count per minute, and the next failure after a quiet minute is logged at once again. At `debug` one `request` line per request: method, target host (`host[:port]`), status (`0`: no response written), `duration_ms`, `bytes` (a tunnel: bytes relayed both ways, logged when it closes), and `error` when refused or failed. Never path, query, tokens or client address.
- **Route mode** (the operator must see how upstreams behave): at `info` one `route summary` line per minute for every route that had traffic, none when idle: route index, upstream host, `requests`, `status` classes, `errors` by reason, `p50_ms` and `p95_ms` (nearest rank over the latest 4096 requests of the minute). At `warn` an upstream state change, once each: `upstream failing` after 5 consecutive upstream errors (with the last reason) and `upstream recovered` on the first response afterwards; a client cancel or a refusal before the upstream does not count. At `debug` one `request` line per request: route index, upstream host, status, `duration_ms`, `bytes`; no query, no client address. A request that matched no route is not logged.
- **Limits:** every level includes the ones above it (`debug` shows `info` and `warn` lines too). The summary interval is fixed at one minute. The last partial interval is flushed on shutdown. Upstream error text is never logged (it can name the dial address). No request IDs exist in the code.
- **Origin:** [#2](https://github.com/denisitpro/lux-l7/pull/2); per-mode logging [#54](https://github.com/denisitpro/lux-l7/issues/54).

### Prometheus metrics

- **What:** `/metrics` (text format, plus Go and process collectors) on its own listener, never on `HTTP_ADDR` or the tunnel listener. Series, all prefixed `lux_l7_`, with the constant label `location` when `METRICS_LOCATION` is set:
  - `requests_total{mode,ingress_host,upstream_host,status_class}`: every proxied request, rejections included; `/healthz`, `/readyz`, `/version` are not counted. `mode` is `header|route|tunnel`; `status_class` is `2xx|3xx|4xx|5xx|error` (`error`: no response written, e.g. client cancel). A tunnel is counted when it opens.
  - `request_duration_seconds{mode,status_class}`: histogram (a tunnel's value is its lifetime).
  - `rejections_total{mode,reason}`: refused before an upstream, reason is a fixed set: `bad_target`, `unauthorized` (header), `proxy_auth_required` (tunnel), `forbidden_host`, `forbidden_port`, `body_too_large` (also a chunked body cut mid-stream), `method_not_allowed`, `no_route`, `bad_hops`, `loop_detected`, `shutting_down`, `tunnel_capacity`, `internal`.
  - `upstream_errors_total{mode,reason}`: `timeout|blocked|canceled|other`, including tunnel dial failures.
  - `tunnels_active`: gauge of open CONNECT tunnels.
- **Cardinality:** bounded by configuration, never by clients; one mapping component (`internal/metrics/labels.go`) decides host labels. `ingress_host`: route mode, the matched route's `host` as written (`other` when nothing matched); header mode, the request `Host` (lowercased, no port) only if listed in `METRICS_INGRESS_HOSTS`, else `other`; tunnel, always `other` (the client names only the target). `upstream_host`: route mode, the route's upstream host; header mode and tunnel, the allow-list entry that admitted the target (exact host, or the suffix entry with its leading dot, e.g. `.example.com`), `other` for an open policy or no match. No path, query, client IP, token, method or full status code is ever a label. Upper bound of `requests_total` per process: sum over modes of `(I + 1) x (U + 1) x 5`, with `I` = ingress hosts (`METRICS_INGRESS_HOSTS` entries, or distinct route hosts) and `U` = upstream hosts (allow-list entries, or distinct route upstream hosts); the tunnel has `I = 0`. Across a fleet multiply by the number of locations.
- **Enable:** `METRICS_ADDR` (full bind address, empty = off, the default); optional `METRICS_LOCATION`, `METRICS_INGRESS_HOSTS`. With metrics off nothing is recorded.
- **Limits:** no auth on the metrics port: scrape from the private network only and never publish it. Go and process series carry no `location` label (set it as a Prometheus target label if needed). Logs and request IDs are out of scope.
- **Origin:** issue [#1](https://github.com/denisitpro/lux-l7/issues/1).

### Tunnel idle timeout

- **What:** a tunnel closes after `TUNNEL_IDLE_TIMEOUT` without bytes in either direction; traffic either way extends it. When one side closes its sending half, the other side is half-closed and still drained.
- **Enable:** always on; default 10m, must be > 0.
- **Limits:** no cap on total tunnel duration.
- **Origin:** [#32](https://github.com/denisitpro/lux-l7/pull/32).

### Tunnel connection cap

- **What:** at most `TUNNEL_MAX_CONNECTIONS` tunnels are open at once. When the cap is reached a new `CONNECT` is refused before the upstream is dialed with `503 tunnel_capacity`, `Retry-After: 1` and `Connection: close`, and counted as the `tunnel_capacity` rejection. The check runs after credentials, host policy and port checks, so an unauthenticated client never learns the proxy is full and cannot fill it. A slot is released when the tunnel ends, and also on a dial failure or a hijack error.
- **Enable:** always on; default `300` (below typical file-descriptor limits: each tunnel holds two sockets). `0` means unlimited; a negative or non-integer value fails startup. Only meaningful with `TUNNEL_ADDR`.
- **Limits:** the cap is per process, not per client or per token. Refused attempts are not queued.
- **Origin:** issue [#62](https://github.com/denisitpro/lux-l7/issues/62).

### Tunnel TLS and certificate reload

- **What:** the tunnel listener can speak TLS 1.2 or higher (ALPN `http/1.1`), so credentials do not cross the network in clear text. The pair is re-read when either file's mtime changes; a failed reload keeps the previous certificate and logs a warning.
- **Enable:** `TUNNEL_TLS_CERT_FILE` and `TUNNEL_TLS_KEY_FILE`, both or neither (one alone fails startup). Optional but recommended.
- **Limits:** without them the listener is plain HTTP and logs a startup warning (local tests only). A pair that cannot be loaded at startup fails startup. Reload is checked on handshake, not by a watcher. The main listener has no TLS option.
- **Origin:** [#32](https://github.com/denisitpro/lux-l7/pull/32).

### Container image and release

- **What:** one static binary (no cgo) in a multi-stage image on a distroless static non-root base; build identity (build id, commit, ref, build time) is linked in. A pushed git tag publishes the image to Docker Hub with an immutable and a mutable tag plus `latest` (`linux/amd64`). CI on `main` runs build, `go test -race`, golangci-lint and govulncheck; Dependabot tracks Go modules, Docker and Actions.
- **Enable:** `make docker` locally; tag push in CI. Release rules are in [`../../CLAUDE.md`](../../CLAUDE.md).
- **Limits:** CI runs only on push to `main`, not on pull requests.
- **Origin:** [#2](https://github.com/denisitpro/lux-l7/pull/2); base image [#29](https://github.com/denisitpro/lux-l7/pull/29); CI/CD [#35](https://github.com/denisitpro/lux-l7/pull/35).

## Explicit non-goals

- MITM TLS or a custom CA on clients (header mode: clients speak TLS to the proxy's ingress, the proxy speaks TLS to the upstream).
- DNS or general L4 tunneling: the tunnel relays TCP only to allow-listed hosts and ports.
- Per-tenant billing or quota.
