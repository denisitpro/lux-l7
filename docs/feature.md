# Feature list

> Status: living · Last verified: 2026-10-05 · [AI - composer-2.5]

L7 egress proxy for mobile and backend API traffic: one stable entrypoint outside restrictive networks; upstream host chosen per request instead of a static ingress `externalName` per target.

## Implemented

- Env-based config, JSON logs, graceful shutdown.
- `GET /healthz`, `GET /readyz`.
- L7 reverse proxy: `X-L7-Target-Host`, optional `X-L7-Target-Scheme` (default from `UPSTREAM_DEFAULT_SCHEME`).
- Upstream allow/deny via `UPSTREAM_ALLOW_HOSTS`, `UPSTREAM_ALLOW_SUFFIXES`, `UPSTREAM_DENY_HOSTS`.
- Auth optional (`AUTH_ENABLED=false` by default): `X-L7-Proxy-Token`, modes `instance` / `per_host`.
- Routing headers stripped before the upstream request.
- Auth **disabled by default** for local and integration tests.

## Planned — core proxy

- WebSocket upgrade passthrough (likely works via std reverse proxy; needs explicit test).

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

- Single static binary, distroless/non-root container image.
- Config entirely from env (12-factor); no reload required for allow-list changes in v1 (restart pod).
- Horizontal scale: stateless replicas behind a Service; no sticky sessions.
- Optional `POD_IP` / standard downward API for logs only.

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

Custom Go service keeps the mobile contract, allow-list envs, and auth in one small codebase aligned with other personal Go repos.
