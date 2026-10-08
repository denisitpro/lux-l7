# Client protocol

> Status: living · Last verified: 2026-10-08 · [AI - claude-sonnet-5-5]

What a client sends and what the proxy answers, per mode. What each feature does and how to enable it: [`capabilities.md`](capabilities.md). Env variables: [`configuration.md`](configuration.md).

## Protocol version and compatibility

This document describes **protocol version 1**. The version is a property of this document, not of the wire: the proxy does not advertise it in any response (`GET /version` returns the build id only).

Rules within a version:

- Changes are **additive only**: new optional request headers, new response headers, new fields in the JSON error body, new error codes, new modes.
- Clients must **ignore** response headers and JSON fields they do not know, and must treat an unknown `error` code like any other error with the same HTTP status class (decide on the status, log the code).
- Existing headers, fields, codes and statuses keep their meaning; checks may get stricter only where the proxy previously forwarded something unsafe.

A breaking change (removed or renamed header, changed meaning, changed status for an existing code) starts protocol version 2. It is announced in this document first, ships beside the old behavior behind an explicit opt-in (a new request header or env switch), and the old behavior is removed only after a deprecation period. No client has to change on upgrade within version 1.

## Error body

Every proxy-generated refusal is JSON with `Content-Type: application/json`:

```json
{"error":"bad_target","detail":"missing X-L7-Target-Host"}
```

`error` is a stable code, `detail` a human sentence (its wording is not a contract). Upstream responses are passed through untouched. The `405` on the tunnel's wrong method also sets `Allow: CONNECT`.

### Telling a proxy error from an upstream error

Every proxy-generated error response carries `X-L7-Proxy: 1`. Upstream responses never do: the proxy removes the header if an upstream sends it. A `403` or `502` with the header was written by the proxy; without it, the upstream wrote it.

It is set on all error envelopes in header, route and tunnel mode. It is **not** set on the probes (`/healthz`, `/readyz`, `/version`) or on the `200 Connection Established` that opens a tunnel. Tunnel clients see it on the refusal response to `CONNECT`.

## Error codes

The single authoritative list. A Go test fails when a code the handlers emit is missing here, or a code listed here is never emitted. Per-mode check order is in the mode sections below.

| `error` | Status | Modes | Meaning |
|---|---|---|---|
| `bad_target` | 400 | header, path, tunnel | Target host, port or scheme is missing or malformed, or `http` is disabled. |
| `unauthorized` | 401 | header, path | Missing or invalid `X-L7-Proxy-Token`. |
| `proxy_auth_required` | 407 | tunnel | Missing or invalid `Proxy-Authorization` (sets `Proxy-Authenticate`). |
| `forbidden` | 403 | header, path, tunnel | Host not allowed, port not allowed, or the resolved upstream address is not public. |
| `no_route` | 404 | route | No route matches host and path. |
| `method_not_allowed` | 405 | route, tunnel | Route mode: `CONNECT`. Tunnel: anything except `CONNECT` (sets `Allow: CONNECT`). |
| `body_too_large` | 413 | header, path, route | Request body above `UPSTREAM_MAX_BYTES`. |
| `bad_hops` | 400 | route | `X-L7-Hops` is not an integer `>= 0`. |
| `loop_detected` | 508 | route | Incoming hops `>= ROUTE_MAX_HOPS`. |
| `upstream_timeout` | 504 | header, path, route, tunnel | Upstream dial or response timed out. |
| `bad_gateway` | 502 | header, path, route, tunnel | Any other upstream dial or transport failure. |
| `shutting_down` | 503 | tunnel | The proxy is draining. |
| `tunnel_capacity` | 503 | tunnel | `TUNNEL_MAX_CONNECTIONS` reached (sets `Retry-After: 1`). |
| `internal` | 500 | tunnel | The connection cannot be hijacked. |

A client that cancels gets no response.

## Probes (all modes)

`GET /healthz` and `GET /readyz` return `200` with body `ok`. `GET /version` returns `{"version":"<build id>"}`. Served locally, no headers needed, never proxied. Any other method or path goes to the mode handler (in path mode only `/h/...` is accepted).

## Header mode

Client sends an ordinary HTTP request to the proxy and names the upstream in headers.

| Header | Required | Meaning |
|---|---|---|
| `X-L7-Target-Host` | yes | Upstream authority `host` or `host:port`, no scheme. IPv6 in brackets; the port may be omitted (`[2001:db8::1]`) and is then the scheme default, like for any host. |
| `X-L7-Target-Scheme` | no | `https` or `http`; default from `UPSTREAM_DEFAULT_SCHEME`. `http` is accepted only when `UPSTREAM_ALLOW_HTTP=true`. |
| `X-L7-Proxy-Token` | when auth is on | Instance token or per-host token. |

The method, path, query, body and all other headers go to the upstream as sent (the three `X-L7-*` headers above and `X-L7-Hops` are removed). The outgoing `Host` is the target authority.

Checks run in this order; the first failure answers:

| # | Check | Status / `error` | `detail` |
|---|---|---|---|
| 1 | Target syntax: host present, no `/ \ ? # @`, port in `1..65535`; scheme is `http` or `https`; `http` allowed | `400 bad_target` | `missing X-L7-Target-Host`, `invalid X-L7-Target-Host`, `invalid X-L7-Target-Host port`, `invalid X-L7-Target-Scheme`, `http upstream scheme is disabled` |
| 2 | Token (only when auth is on; `per_host` keys on the parsed target host) | `401 unauthorized` | `missing or invalid proxy token` |
| 3 | Host policy | `403 forbidden` | `upstream host not allowed` |
| 4 | Port allow-list (implicit 443 for https, 80 for http) | `403 forbidden` | `upstream port not allowed` |
| 5 | Body size (`Content-Length` above `UPSTREAM_MAX_BYTES`) | `413 body_too_large` | `request body too large` |
| 6 | Dial: resolved IP not public | `403 forbidden` | `upstream address not allowed` |
| 7 | Dial or round trip: timeout | `504 upstream_timeout` | `upstream request timed out` |
| 8 | Dial or round trip: other failure | `502 bad_gateway` | `upstream request failed` |

Notes: the host is lowercased and one trailing dot is dropped before policy, auth and dial. The token is checked before policy and port, so an unauthenticated client gets `401` whatever it targets and cannot probe the allow list or the port list. A client that cancels gets no response.

## Path mode

For clients that cannot set headers. The target is the first path segment after `/h/`:

```
GET /h/api.example.com/v1/items?limit=5 HTTP/1.1
X-L7-Proxy-Token: <token>          (only when auth is on)
```

is forwarded as `GET https://api.example.com/v1/items?limit=5`. Form: `/h/<host>[:port]/<path>?<query>`; IPv6 in brackets (`/h/[2001:db8::1]:443/x`, port optional: `/h/[2001:db8::1]/x`). The scheme is `UPSTREAM_DEFAULT_SCHEME`; there is no per-request scheme. `X-L7-Target-Host`, `X-L7-Target-Scheme` and `X-L7-Proxy-Token` are ignored for routing and removed before the upstream. The token is read from the header only, never from path or query. Path and query go on verbatim (`%2F` stays encoded); an empty path (`/h/api.example.com`) is `/`. The outgoing `Host` is the target authority.

Checks and status codes are those of header mode, in the same order. Check 1 differs: `400 bad_target` with `detail` `path must start with /h/<host>`, `missing target host in path`, `invalid target host in path` (also for a percent-encoded host), `invalid target host in path port`, or `http upstream scheme is disabled`.

Response rewriting: a `Location` or `Content-Location` value that is an absolute URL to the target (same scheme, host and effective port; host compared case-insensitively), a `//host/...` URL to it, or an absolute path (`/x`) becomes `/h/<host>[:port]/...` (query and fragment kept). Other hosts, other schemes, URLs with userinfo and relative values (`x`, `?q`) are untouched. `Set-Cookie` and response bodies are not rewritten ([#51](https://github.com/denisitpro/lux-l7/issues/51)).

## CONNECT tunnel

A separate listener (`TUNNEL_ADDR`). It speaks HTTP/1.1, optionally over TLS to the proxy, and accepts only `CONNECT`.

```
CONNECT api.example.com:443 HTTP/1.1
Host: api.example.com:443
Proxy-Authorization: Basic base64(user:token)
```

`user` is ignored; `token` is validated like `X-L7-Proxy-Token` (per-host mode is keyed by the CONNECT host). No credentials are needed when auth is off. After `HTTP/1.1 200 Connection Established` the connection carries raw bytes; the client then starts TLS to the target.

| # | Check | Status / `error` | `detail` |
|---|---|---|---|
| 0 | Method is not `CONNECT` | `405 method_not_allowed` | `only CONNECT is supported on the tunnel listener` |
| 1 | Authority is `host:port` with a valid port, no userinfo | `400 bad_target` | `CONNECT target must be host:port` |
| 2 | Credentials (only when auth is on) | `407 proxy_auth_required` (header `Proxy-Authenticate: Basic realm="lux-l7"`) | `missing or invalid proxy credentials` |
| 3 | Host policy | `403 forbidden` | `upstream host not allowed` |
| 4 | `TUNNEL_ALLOW_PORTS` | `403 forbidden` | `upstream port not allowed` |
| 5 | Proxy is shutting down | `503 shutting_down` | `proxy is shutting down` |
| 6 | `TUNNEL_MAX_CONNECTIONS` tunnels already open (checked after 2 to 4 and before the dial) | `503 tunnel_capacity` (headers `Retry-After: 1`, `Connection: close`) | `tunnel capacity reached, retry shortly` |
| 7 | Dial: blocked IP / timeout / other | `403 forbidden` / `504 upstream_timeout` / `502 bad_gateway` | as in header mode |
| 8 | Connection cannot be hijacked | `500 internal` | `connection cannot be tunneled` |

`200 Connection Established` is sent only after the upstream dial succeeded. Bytes the client sent right behind the `CONNECT` (the TLS ClientHello) are forwarded first.

Local check through a TLS tunnel listener:

```sh
openssl req -x509 -newkey ec -pkeyopt ec_paramgen_curve:prime256v1 -nodes -days 1 \
  -keyout tls.key -out tls.crt -subj /CN=localhost -addext "subjectAltName=IP:127.0.0.1"
TUNNEL_ADDR=127.0.0.1:8443 TUNNEL_TLS_CERT_FILE=tls.crt TUNNEL_TLS_KEY_FILE=tls.key \
UPSTREAM_ALLOW_SUFFIXES=example.com go run ./cmd/lux-l7
curl --proxy https://127.0.0.1:8443 --proxy-cacert tls.crt https://example.com/
```

## Route mode

Client sends an ordinary request; the proxy picks the upstream from the `Host` header and the path. No `X-L7-*` routing headers and no auth.

| Check | Status / `error` | `detail` |
|---|---|---|
| Method is `CONNECT` | `405 method_not_allowed` | `CONNECT is not supported in route mode` |
| No route matches host + path | `404 no_route` | `no matching route` |
| `X-L7-Hops` is not an integer `>= 0` | `400 bad_hops` | `invalid X-L7-Hops` |
| Incoming hops `>= ROUTE_MAX_HOPS` | `508 loop_detected` | `hop limit exceeded` |
| Body size | `413 body_too_large` | `request body too large` |
| Upstream timeout / other failure | `504 upstream_timeout` / `502 bad_gateway` | as above |

Matching: the request `Host` is lowercased, port and one trailing dot removed, then compared with the route `host` (exact, or a leading-dot suffix that also matches the bare domain). `path_prefix` matches on a segment boundary (`/api` matches `/api` and `/api/x`, not `/apix`); `/` matches everything. First match in `ROUTES` order wins. With `rewrite_prefix` set, the matched prefix is replaced by it; the query string is kept.

The upstream request carries `X-L7-Hops` incremented by one, `X-Forwarded-For`, `X-Forwarded-Host` and `X-Forwarded-Proto`; the `X-L7-Target-*` and `X-L7-Proxy-Token` headers are removed. The route table format is in [`configuration/routes.md`](configuration/routes.md).
