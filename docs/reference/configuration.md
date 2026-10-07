# Configuration

> Status: living · Last verified: 2026-10-06 · [AI - claude-sonnet-5-5]

The guide for people who deploy lux-l7. Everything is configured through environment variables, verified against `internal/config`. This page has the common rules, ready-made recipes and an index of every variable; each variable is explained in a topic page (what it does, accepted values, default, when to change it, an example, what happens when it is wrong). What the features do: [`capabilities.md`](capabilities.md). Copy-paste defaults: [`.env.example`](../../.env.example).

Nothing is required to start. With no variables the service listens on `:8080` in header mode with auth off and any public host reachable, which is only fit for a local run.

## Common rules

- **Blank is unset.** An empty or whitespace-only value is the same as not setting the variable: the default applies.
- **Where values come from.** The process environment, plus an optional `test.env` file in the working directory. `test.env` is read first and never overrides a variable already set in the environment, so a real deployment variable always wins.
- **Durations** use Go syntax: `500ms`, `30s`, `10m`, `1h30m`. `0` is a valid duration; days (`1d`) are not. An unparsable value fails startup.
- **Booleans** accept `true`/`false`/`1`/`0` (also `t`, `f`, `TRUE`, `FALSE`, and similar Go spellings). Anything else, such as `yes`, fails startup instead of silently picking a default.
- **Lists** are comma-separated; items are trimmed and lowercased, empty items dropped.
- **Ports** in port lists are integers from `1` to `65535`.
- **JSON values** (`ROUTES`, `PROXY_AUTH_HOST_MAP`) go on one line.
- **Fail fast.** An invalid value or an unsafe combination exits the process at startup with a message naming the variable. Mode-specific variables (`UPSTREAM_ALLOW_PORTS`, `TUNNEL_ALLOW_PORTS`, `UPSTREAM_ALLOW_HTTP`, `UPSTREAM_ALLOW_PRIVATE`, `UPSTREAM_FORWARD_CLIENT_IP`, `UPSTREAM_TRUSTED_PROXIES`, `UPSTREAM_DEFAULT_SCHEME`, `ROUTE_MAX_HOPS`) are checked in every mode, so a typo fails even where the variable has no effect; their errors end with `(checked in every PROXY_MODE, ...)`. Softer problems are logged once as warnings after startup (see [`configuration/listener.md`](configuration/listener.md#log_level)).
- **No hot reload.** Change a variable, restart the process. The only exception is the tunnel TLS certificate pair.

## Recipes

Hostnames, tokens and paths below are placeholders.

### Minimal local run

```sh
cp .env.example test.env   # optional; everything in it is commented out
go run ./cmd/lux-l7
curl -H 'X-L7-Target-Host: example.com' http://localhost:8080/
```

### Header mode for production

A token so only your clients can use the proxy, and an allow list so they can reach only what they need:

```sh
PROXY_AUTH_TOKEN=<output of: openssl rand -hex 32>
UPSTREAM_ALLOW_HOSTS=api.example.com
UPSTREAM_ALLOW_SUFFIXES=.example.net
UPSTREAM_ALLOW_PORTS=443
```

Clients send `X-L7-Proxy-Token` and `X-L7-Target-Host`. Put TLS in front (ingress or load balancer): the main listener is plain HTTP. See [`configuration/modes-and-auth.md`](configuration/modes-and-auth.md) and [`configuration/upstream-policy.md`](configuration/upstream-policy.md).

### Route mode

A fixed table instead of client-chosen targets; no auth, so keep it on a trusted network:

```sh
PROXY_MODE=route
ROUTES=[{"host":"example.com","path_prefix":"/eu-1","upstream":"https://origin.example.com:443","rewrite_prefix":"/"}]
```

Details and every route field: [`configuration/routes.md`](configuration/routes.md).

### Path mode

For clients that cannot set headers: the target is the path, `/h/<host>[:port]/<path>`. Same token, allow list and guards as header mode:

```sh
PROXY_MODE=path
PROXY_AUTH_TOKEN=<output of: openssl rand -hex 32>
UPSTREAM_ALLOW_HOSTS=api.example.com
```

Clients call `/h/api.example.com/v1/items` with `X-L7-Proxy-Token`. See [`configuration/modes-and-auth.md`](configuration/modes-and-auth.md#proxy_mode).

### CONNECT tunnel with TLS

An extra listener for end-to-end TLS to allow-listed targets:

```sh
PROXY_AUTH_TOKEN=<token>
UPSTREAM_ALLOW_SUFFIXES=.example.com
TUNNEL_ADDR=:8443
TUNNEL_ALLOW_PORTS=443,8443
TUNNEL_TLS_CERT_FILE=/etc/lux-l7/tls.crt
TUNNEL_TLS_KEY_FILE=/etc/lux-l7/tls.key
```

Expose the tunnel port at L4 (LoadBalancer or TLS passthrough). Details: [`configuration/tunnel.md`](configuration/tunnel.md).

### Metrics

```sh
METRICS_ADDR=:9090
METRICS_LOCATION=eu-1
METRICS_INGRESS_HOSTS=front.example.com
```

Scrape `/metrics` from the private network only. Details: [`configuration/metrics.md`](configuration/metrics.md).

## Variable index

Every variable the code reads. Parenthesised modes say where a variable has an effect; path mode behaves like header mode unless noted.

| Variable | Default | Purpose | Details |
|---|---|---|---|
| `PROXY_MODE` | `header` | `header`, `route` or `path`: how the upstream is chosen | [modes-and-auth](configuration/modes-and-auth.md#proxy_mode) |
| `PROXY_AUTH_TOKEN` | unset | One or several shared access tokens (header, path, tunnel) | [modes-and-auth](configuration/modes-and-auth.md#proxy_auth_token) |
| `PROXY_AUTH_HOST_MAP` | unset | JSON map of target host to token (header, tunnel) | [modes-and-auth](configuration/modes-and-auth.md#proxy_auth_host_map) |
| `AUTH_ENABLED`, `AUTH_MODE` | removed | Ignored with a startup warning | [modes-and-auth](configuration/modes-and-auth.md#auth_enabled-auth_mode-removed) |
| `HTTP_ADDR` | `:8080` | Main listener address | [listener](configuration/listener.md#http_addr) |
| `HTTP_READ_HEADER_TIMEOUT` | `5s` | Time allowed to send request headers (also tunnel) | [listener](configuration/listener.md#http_read_header_timeout) |
| `HTTP_READ_TIMEOUT` | `0` (off) | Cap on reading a whole request; cuts streams if set | [listener](configuration/listener.md#http_read_timeout) |
| `HTTP_WRITE_TIMEOUT` | `0` (off) | Cap on writing a whole response; cuts streams if set | [listener](configuration/listener.md#http_write_timeout) |
| `HTTP_IDLE_TIMEOUT` | `120s` | Keep-alive idle time of client connections | [listener](configuration/listener.md#http_idle_timeout) |
| `SHUTDOWN_TIMEOUT` | `15s` | Drain time on SIGINT/SIGTERM | [listener](configuration/listener.md#shutdown_timeout) |
| `LOG_LEVEL` | `info` | `debug`, `info`, `warn`, `error` | [listener](configuration/listener.md#log_level) |
| `UPSTREAM_TIMEOUT` | `30s` | Wait for upstream response headers; tunnel dial timeout | [upstream-policy](configuration/upstream-policy.md#upstream_timeout) |
| `UPSTREAM_MAX_BYTES` | `33554432` | Largest request body (every mode) | [upstream-policy](configuration/upstream-policy.md#upstream_max_bytes) |
| `UPSTREAM_DEFAULT_SCHEME` | `https` | Scheme when the client names none (header, path) | [upstream-policy](configuration/upstream-policy.md#upstream_default_scheme) |
| `UPSTREAM_ALLOW_HTTP` | `false` | Permit plain-http upstreams (header, path) | [upstream-policy](configuration/upstream-policy.md#upstream_allow_http) |
| `UPSTREAM_ALLOW_PRIVATE` | `false` | `true` disables the SSRF guard (header, path, tunnel) | [upstream-policy](configuration/upstream-policy.md#upstream_allow_private) |
| `UPSTREAM_FORWARD_CLIENT_IP` | `false` | Send `X-Forwarded-For/Host/Proto` to the upstream (header, path) | [upstream-policy](configuration/upstream-policy.md#upstream_forward_client_ip) |
| `UPSTREAM_TRUSTED_PROXIES` | empty | IPs/CIDRs allowed to supply inbound `X-Forwarded-*`; empty trusts all (header, path) | [upstream-policy](configuration/upstream-policy.md#upstream_trusted_proxies) |
| `UPSTREAM_ALLOW_PORTS` | `80,443,8443,9443` | Reachable target ports (header, path) | [upstream-policy](configuration/upstream-policy.md#upstream_allow_ports) |
| `UPSTREAM_ALLOW_HOSTS` | empty | Exact hostnames admitted (header, path, tunnel) | [upstream-policy](configuration/upstream-policy.md#upstream_allow_hosts) |
| `UPSTREAM_ALLOW_SUFFIXES` | empty | Domain suffixes admitted (header, path, tunnel) | [upstream-policy](configuration/upstream-policy.md#upstream_allow_suffixes) |
| `UPSTREAM_DENY_HOSTS` | empty | Hostnames always refused (header, path, tunnel) | [upstream-policy](configuration/upstream-policy.md#upstream_deny_hosts) |
| `ROUTES` | unset | JSON route table; required in route mode | [routes](configuration/routes.md#routes) |
| `ROUTE_MAX_HOPS` | `1` | Loop guard limit (route) | [routes](configuration/routes.md#route_max_hops) |
| `TUNNEL_ADDR` | empty (off) | CONNECT tunnel listen address (header, path) | [tunnel](configuration/tunnel.md#tunnel_addr) |
| `TUNNEL_ALLOW_PORTS` | `443` | Reachable target ports of the tunnel | [tunnel](configuration/tunnel.md#tunnel_allow_ports) |
| `TUNNEL_IDLE_TIMEOUT` | `10m` | Close a tunnel after this idle time | [tunnel](configuration/tunnel.md#tunnel_idle_timeout) |
| `TUNNEL_MAX_CONNECTIONS` | `300` | Cap on concurrent tunnels, `0` = unlimited | [tunnel](configuration/tunnel.md#tunnel_max_connections) |
| `TUNNEL_TLS_CERT_FILE`, `TUNNEL_TLS_KEY_FILE` | empty | PEM pair enabling TLS on the tunnel listener | [tunnel](configuration/tunnel.md#tunnel_tls_cert_file-tunnel_tls_key_file) |
| `METRICS_ADDR` | empty (off) | Prometheus listener address | [metrics](configuration/metrics.md#metrics_addr) |
| `METRICS_LOCATION` | empty | Constant `location` label on metrics | [metrics](configuration/metrics.md#metrics_location) |
| `METRICS_INGRESS_HOSTS` | empty | Front hosts allowed as `ingress_host` label (header, path) | [metrics](configuration/metrics.md#metrics_ingress_hosts) |

## Combinations that fail startup

| Setting | Conflict |
|---|---|
| `PROXY_AUTH_TOKEN` and `PROXY_AUTH_HOST_MAP` | choose one |
| a token variable with `PROXY_MODE=route` | route mode has no auth |
| `TUNNEL_ADDR` with `PROXY_MODE=route` | route mode has no tunnel (header and path mode allow it) |
| `TUNNEL_ADDR` with no token and no allow list | open relay refused |
| one `TUNNEL_TLS_*` file without the other | both or neither |
| `ROUTES` with `PROXY_MODE=header` or `path`, or missing in route mode | `ROUTES` belongs to route mode only |
| `UPSTREAM_DEFAULT_SCHEME=http` without `UPSTREAM_ALLOW_HTTP=true` | header and path mode refuse a silent downgrade |
| a host list entry with a port (header and path mode) | lists hold bare hostnames |
| `HTTP_ADDR` and `TUNNEL_ADDR` on one socket | one listener per socket (rule below) |
| `METRICS_ADDR` on the socket of `HTTP_ADDR` or `TUNNEL_ADDR`, or with port `0` | metrics never share a proxy socket and need a fixed port |
| `HTTP_READ_HEADER_TIMEOUT` or `HTTP_IDLE_TIMEOUT` of zero or less | the slow-header and keep-alive bounds would vanish |

**Socket clash rule.** Two listen addresses clash when the ports are equal and the hosts are equal or either one is a wildcard (empty, `0.0.0.0` or `::`): `:8080`, `0.0.0.0:8080`, `[::]:8080` and `127.0.0.1:8080` all clash with each other, `127.0.0.1:8080` and `127.0.0.2:8080` do not. Hostnames are not resolved, so `localhost:8080` and `127.0.0.1:8080` are not compared; the second bind then fails when listening starts. An unparsable address is left to the listener to reject.


Warnings only (logged at startup, the process runs): empty allow list in header and path mode, auth off in header and path mode, host lists set in route mode (ignored), `UPSTREAM_ALLOW_PRIVATE=true` (header mode, path mode or tunnel), a tunnel without TLS, `METRICS_LOCATION` or `METRICS_INGRESS_HOSTS` without `METRICS_ADDR`, `METRICS_INGRESS_HOSTS` in route mode, `UPSTREAM_TRUSTED_PROXIES` without `UPSTREAM_FORWARD_CLIENT_IP`, either of the two in route mode (ignored), the removed `AUTH_*` variables, and a route `tls_server_name` that differs from the outgoing `Host`.
