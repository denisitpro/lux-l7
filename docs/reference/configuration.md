# Configuration

> Status: living · Last verified: 2026-10-06 · [AI - claude-sonnet-5-5]

Every environment variable, verified against `internal/config`. Copy-paste defaults live in [`.env.example`](../../.env.example); this file owns the explanations. What the features do: [`capabilities.md`](capabilities.md).

Rules for all variables: unset or blank means the default; an optional `test.env` in the working directory is loaded first and never overrides variables already set; durations use Go syntax (`30s`, `10m`); booleans accept `true`/`false`/`1`/`0` and anything else fails startup; list values are comma-separated, trimmed and lowercased.

Nothing is required to start. With no variables the service runs in header mode on `:8080`, auth off.

## Required per feature

| Feature | Variables | Notes |
|---|---|---|
| Auth, `instance` model | `PROXY_AUTH_TOKEN` | One token or a comma-separated list. Auth turns on by this variable alone. |
| Auth, `per_host` model | `PROXY_AUTH_HOST_MAP` | JSON object host to token, e.g. `{"api.example.com":"token"}`. Needs at least one non-empty entry. |
| CONNECT tunnel | `TUNNEL_ADDR` and one of: a token variable above, or `UPSTREAM_ALLOW_HOSTS` / `UPSTREAM_ALLOW_SUFFIXES` | Without either, startup fails (an open relay is refused). |
| Tunnel TLS | `TUNNEL_TLS_CERT_FILE` and `TUNNEL_TLS_KEY_FILE` | Both or neither. Recommended, not required. |
| Route mode | `PROXY_MODE=route` and `ROUTES` | `ROUTES` is an error in header mode. |

## Optional

### Mode and auth

| Variable | Default | Meaning |
|---|---|---|
| `PROXY_MODE` | `header` | `header` or `route`. |
| `PROXY_AUTH_TOKEN` | unset | See above. Both token variables set is a startup error; either set in route mode is a startup error. |
| `PROXY_AUTH_HOST_MAP` | unset | See above. |
| `AUTH_ENABLED`, `AUTH_MODE` | removed | Both are ignored (value never parsed) and log a startup warning that states the actual auth state. |

### Listener and shutdown

| Variable | Default | Meaning |
|---|---|---|
| `HTTP_ADDR` | `:8080` | Main listener address. |
| `HTTP_READ_HEADER_TIMEOUT` | `5s` | Also bounds reading the `CONNECT` request on the tunnel. |
| `HTTP_READ_TIMEOUT` | `0` (off) | Main listener only. Caps the whole request including the body; a non-zero value cuts slow uploads. `0` is allowed, negative fails startup. |
| `HTTP_WRITE_TIMEOUT` | `0` (off) | Main listener only. Caps the whole response; a non-zero value cuts SSE, long downloads and WebSocket after upgrade. `0` is allowed, negative fails startup. |
| `HTTP_IDLE_TIMEOUT` | `120s` | Main listener keep-alive idle time. |
| `SHUTDOWN_TIMEOUT` | `15s` | Drain budget for both listeners; must be > 0. |
| `LOG_LEVEL` | `info` | `debug`, `info`, `warn` (or `warning`), `error`; anything else fails startup. |

### Upstream (header mode unless stated)

| Variable | Default | Meaning |
|---|---|---|
| `UPSTREAM_TIMEOUT` | `30s` | Response-header wait for upstream requests in both modes, and the dial timeout of the tunnel; must be > 0. |
| `UPSTREAM_MAX_BYTES` | `33554432` | Request body cap, header and route mode; must be > 0. |
| `UPSTREAM_DEFAULT_SCHEME` | `https` | Scheme when the client sends none; `http` or `https`. `http` requires `UPSTREAM_ALLOW_HTTP=true`. |
| `UPSTREAM_ALLOW_HTTP` | `false` | Permit plain-http upstreams. |
| `UPSTREAM_ALLOW_PRIVATE` | `false` | `true` disables the SSRF guard for header mode and the tunnel (startup warning in header mode). Local tests only. |
| `UPSTREAM_ALLOW_PORTS` | `80,443,8443,9443` | Reachable target ports; each `1..65535`, at least one. |
| `UPSTREAM_ALLOW_HOSTS` | empty | Exact hostnames admitted. Bare hostnames only; an entry with a port fails startup. Also applies to the tunnel. |
| `UPSTREAM_ALLOW_SUFFIXES` | empty | Domain suffixes admitted (apex and subdomains). Same rules. |
| `UPSTREAM_DENY_HOSTS` | empty | Exact hostnames refused; wins over the allow lists. Same rules. |

With both allow lists empty every host is admitted (header mode logs a warning). In route mode the three policy variables are ignored with a warning, and `UPSTREAM_ALLOW_HTTP`, `UPSTREAM_ALLOW_PRIVATE` and `UPSTREAM_ALLOW_PORTS` have no effect.

### Route mode

| Variable | Default | Meaning |
|---|---|---|
| `ROUTE_MAX_HOPS` | `1` | Incoming `X-L7-Hops` at or above this is `508 loop_detected`; must be >= 1. |

`ROUTES` is a JSON array, order matters (first match wins), unknown fields fail startup:

| Field | Required | Meaning |
|---|---|---|
| `host` | yes | Exact host, or a leading-dot suffix (`.example.com`, also matches `example.com`). |
| `path_prefix` | yes | Must start with `/`; matched on a segment boundary; `/` matches all. |
| `upstream` | yes | `http` or `https` origin, host with optional port; no userinfo, path, query or fragment. |
| `rewrite_prefix` | no | Replaces the matched prefix; must start with `/` when set. |
| `host_header` | no | Outgoing `Host` (port kept as written). Defaults to the `upstream` authority (host plus port), not the route `host`; set it to the route host when the upstream expects the original site host. |
| `tls_server_name` | no | SNI override, https upstreams only. Defaults to the outgoing `Host` without port. |

```json
[
  {"host":"example.com","path_prefix":"/local","upstream":"http://127.0.0.1:9001","rewrite_prefix":"/"},
  {"host":"example.com","path_prefix":"/region1","upstream":"https://origin.example.com:443","rewrite_prefix":"/"},
  {"host":"example.com","path_prefix":"/direct","upstream":"https://203.0.113.10:443","host_header":"example.com","rewrite_prefix":"/"},
  {"host":".example.com","path_prefix":"/","upstream":"https://origin.example.com:443"}
]
```

### Metrics

| Variable | Default | Meaning |
|---|---|---|
| `METRICS_ADDR` | empty (off) | Full bind address for `/metrics`, port required (`:9090`, `127.0.0.1:9090`). Must differ from `HTTP_ADDR` and `TUNNEL_ADDR`. Never publish it. |
| `METRICS_LOCATION` | empty | Constant `location` label on every `lux_l7_` series, e.g. `eu-1`: 1-32 characters of letters, digits, `.`, `_`, `-`, starting with a letter or digit. Empty: no label (set it as a Prometheus target label instead). |
| `METRICS_INGRESS_HOSTS` | empty | Header mode: front hosts allowed as `ingress_host` label values (bare hostnames, no port); any other `Host` is `other`. Ignored in route mode (labels come from `ROUTES`). |

`METRICS_LOCATION` and `METRICS_INGRESS_HOSTS` without `METRICS_ADDR` log a startup warning. Series and the cardinality bound: [`capabilities.md`](capabilities.md#prometheus-metrics).

### Tunnel

| Variable | Default | Meaning |
|---|---|---|
| `TUNNEL_ADDR` | empty (off) | Tunnel listen address, e.g. `:8443`. Header mode only. |
| `TUNNEL_ALLOW_PORTS` | `443` | Reachable target ports; add `8443,9443` for exchange WebSocket streams. |
| `TUNNEL_IDLE_TIMEOUT` | `10m` | Idle limit per tunnel, reset by traffic either way; must be > 0. |
| `TUNNEL_TLS_CERT_FILE`, `TUNNEL_TLS_KEY_FILE` | empty | PEM pair for TLS to the proxy; reloaded when either file's mtime changes. |

## Auth token notes

Generate each token with `openssl rand -hex 32` (64 hex characters). A token cannot contain a comma. One env string is capped at 128 KiB on Linux and a Kubernetes Secret at 1 MiB, which fits about two thousand 64-character tokens; the per-request cost is one SHA-256 plus one 32-byte compare per token.

## Startup errors

The process exits at startup, never degrades silently, on: an unparsable boolean, duration, integer or port; a duration or byte limit that must be > 0 and is not; unknown `PROXY_MODE`, `UPSTREAM_DEFAULT_SCHEME`, or `LOG_LEVEL`; `ROUTES` set in header mode or missing or invalid in route mode; both token variables set; a token variable in route mode; `TUNNEL_ADDR` in route mode; an open tunnel (no auth and no allow list); one TLS file without the other or an unreadable pair; policy entries with a port; an invalid `METRICS_ADDR` (no port, equal to `HTTP_ADDR` or `TUNNEL_ADDR`), `METRICS_LOCATION` or `METRICS_INGRESS_HOSTS` entry.
