# Client protocol

> Status: living · Last verified: 2026-10-06 · [AI - claude-sonnet-5-5]

What a client sends and what the proxy answers, per mode. What each feature does and how to enable it: [`capabilities.md`](capabilities.md). Env variables: [`configuration.md`](configuration.md).

## Error body

Every proxy-generated refusal is JSON with `Content-Type: application/json`:

```json
{"error":"bad_target","detail":"missing X-L7-Target-Host"}
```

`error` is a stable code, `detail` a human sentence. Upstream responses are passed through untouched. The `405` on the tunnel's wrong method also sets `Allow: CONNECT`.

## Probes (all modes)

`GET /healthz` and `GET /readyz` return `200` with body `ok`. `GET /version` returns `{"version":"<build id>"}`. Served locally, no headers needed, never proxied. Any other method or path goes to the mode handler.

## Header mode

Client sends an ordinary HTTP request to the proxy and names the upstream in headers.

| Header | Required | Meaning |
|---|---|---|
| `X-L7-Target-Host` | yes | Upstream authority `host` or `host:port`, no scheme. IPv6 in brackets. |
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
| 6 | Dial: blocked IP / timeout / other | `403 forbidden` / `504 upstream_timeout` / `502 bad_gateway` | as in header mode |
| 7 | Connection cannot be hijacked | `500 internal` | `connection cannot be tunneled` |

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
