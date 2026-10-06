# Route mode

> Status: living · Last verified: 2026-10-06 · [AI - claude-sonnet-5-5]

The static route table used with `PROXY_MODE=route`. Part of the [configuration guide](../configuration.md). Route mode behaviour and limits: [`../capabilities.md`](../capabilities.md#route-mode).

In route mode clients send ordinary requests. The proxy takes the request `Host` and path, finds the first matching route and forwards to that route's upstream. No routing headers are read. A request that matches nothing is `404 no_route`.

## Variables

### `ROUTES`

- **What:** the route table, a JSON array of route objects.
- **Values:** a one-line JSON array with at least one route; fields below. Order matters: the first matching route wins, so put specific paths before a catch-all. Unknown fields fail startup, so a typo is caught.
- **Default:** unset. Required when `PROXY_MODE=route`.
- **Change it when:** you add or move an origin. A change needs a restart.
- **If wrong:** missing or invalid JSON in route mode fails startup, and the error names the route (`ROUTES[2]: ...`). Setting it in header mode also fails startup, so a stale variable cannot silently do nothing.

### `ROUTE_MAX_HOPS`

- **What:** loop protection. Each route-mode hop increments the `X-L7-Hops` header; a request arriving with a count at or above this limit is refused with `508 loop_detected`. A non-integer or negative header is `400 bad_hops`.
- **Values:** an integer, at least 1.
- **Default:** `1`: a request that already went through one route hop is refused.
- **Change it when:** you deliberately chain route-mode instances; set it to the number of hops you allow.
- **Example:** `ROUTE_MAX_HOPS=2`
- **If wrong:** below 1 or not an integer fails startup. The value is parsed in every mode, so a bad value fails startup even in header mode, where it has no effect.

## Route fields

| Field | Required | Meaning |
|---|---|---|
| `host` | yes | The incoming `Host` to match. Exact (`example.com`), or a leading-dot suffix (`.example.com`) that matches the apex and every subdomain. Case-insensitive; a port or trailing dot in the incoming `Host` is ignored. |
| `path_prefix` | yes | Must start with `/`. Matches on a segment boundary: `/api` matches `/api` and `/api/x`, not `/apix`. `/` matches everything. |
| `upstream` | yes | Origin to forward to: `http://` or `https://`, host and optional port. No userinfo, path, query or fragment. |
| `rewrite_prefix` | no | Replaces the matched `path_prefix`; must start with `/` when set. `/` strips the prefix. Empty or absent leaves the path unchanged. |
| `host_header` | no | The `Host` sent to the upstream, port kept as written. Default: the `upstream` host plus its port, not the route `host`. |
| `tls_server_name` | no | TLS SNI for an https upstream. Default: the outgoing `Host` without port. Allowed only with an https `upstream`. |

Things to know:

- **Outgoing `Host`.** By default the upstream sees its own authority (`origin.example.com:8443`), not the site name the client used. If the origin serves several sites and needs the original name, set `host_header` to the route host. Each route has a fixed outgoing `Host`, and the SNI equals it unless you set `tls_server_name`.
- **SNI mismatch.** A `tls_server_name` that differs from the outgoing `Host` logs a startup warning: CDN-fronted origins often reject that combination.
- **Direct IP upstream.** For `upstream: https://203.0.113.10:443` set `host_header` so the origin sees a real site name and the SNI is that name.
- **Proxy variables.** Route upstreams are dialled directly; `HTTP_PROXY`, `HTTPS_PROXY` and `NO_PROXY` are ignored.
- **Forwarding headers.** The proxy sets `X-Forwarded-For`, `X-Forwarded-Host` and `X-Forwarded-Proto`, replacing any client-sent values.
- **Trust.** Route mode applies no auth, host policy, SSRF guard or port allow-list, so keep it reachable only by clients you accept. `UPSTREAM_MAX_BYTES` and `UPSTREAM_TIMEOUT` still apply ([`upstream-policy.md`](upstream-policy.md)).

## Example

```json
[
  {"host":"example.com","path_prefix":"/local","upstream":"http://127.0.0.1:9001","rewrite_prefix":"/"},
  {"host":"example.com","path_prefix":"/eu-1","upstream":"https://origin.example.com:443","rewrite_prefix":"/"},
  {"host":"example.com","path_prefix":"/direct","upstream":"https://203.0.113.10:443","host_header":"example.com","rewrite_prefix":"/"},
  {"host":".example.com","path_prefix":"/","upstream":"https://origin.example.com:443"}
]
```

A request to `example.com/eu-1/v1/items` goes to `https://origin.example.com:443/v1/items`. The catch-all route is last, otherwise it would shadow the others.
