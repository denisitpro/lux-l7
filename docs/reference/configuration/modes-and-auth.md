# Mode and authentication

> Status: living · Last verified: 2026-10-06 · [AI - claude-sonnet-5-5]

Which way the proxy picks the upstream, and who may use it. Part of the [configuration guide](../configuration.md). What each mode does for clients: [`../capabilities.md`](../capabilities.md#modes); the wire contract: [`../protocol.md`](../protocol.md).

## Mode

### `PROXY_MODE`

- **What:** selects how the upstream is chosen. `header`: the client names it per request in `X-L7-Target-Host`. `path`: the client puts it in the URL, `/h/<host>[:port]/<path>`, for clients that cannot set headers. `route`: a static table (`ROUTES`) maps the incoming host and path to an upstream, and clients send ordinary requests.
- **Values:** `header`, `path` or `route`; case-insensitive.
- **Default:** `header`.
- **Change it when:** `path` if clients cannot send `X-L7-Target-Host`; `route` for a fixed hostname/path to origin mapping instead of client-chosen targets (see [`routes.md`](routes.md)).
- **Example:** `PROXY_MODE=path` or `PROXY_MODE=route`
- **If wrong:** any other value fails startup. Combinations that fail startup: `ROUTES` set in header or path mode; `ROUTES` missing in route mode; a token variable in route mode; `TUNNEL_ADDR` in route mode.
- **Path mode:** everything header mode does (token in `X-L7-Proxy-Token`, host policy, SSRF guard, port allow-list, body limit, tunnel, `UPSTREAM_*` variables), except that the target is the first path segment and the scheme is always `UPSTREAM_DEFAULT_SCHEME` (`X-L7-Target-*` are ignored and stripped). Redirect `Location` headers to the target are rewritten back to `/h/<host>/...`; `Set-Cookie` is not ([#51](https://github.com/denisitpro/lux-l7/issues/51)). The token never goes in the path or query: a client that cannot set headers runs without auth and relies on the allow list. Wire form: [`../protocol.md`](../protocol.md#path-mode).
- **Route mode drops:** auth, the tunnel, the SSRF guard and the port allow-list; the allow and deny lists are ignored with a startup warning. Upstreams there are operator-written, so they are trusted.

## Authentication

Auth is off unless a token variable is set; there is no separate on/off switch. The variable you set chooses the model. Auth applies to header mode, path mode and the tunnel, never to route mode.

A client presents the token as `X-L7-Proxy-Token` in header or path mode, or as the password of `Proxy-Authorization: Basic` on the tunnel (the user part is ignored). The token is checked before the host policy and port, so an unauthenticated client cannot probe the allow list.

Generate each token with `openssl rand -hex 32` (64 hex characters). Treat tokens as secrets: keep them in a secret store, never in git. Only SHA-256 hashes are kept in memory, and startup logs the token count but never values. Revoking a token means removing it and redeploying.

### `PROXY_AUTH_TOKEN`

- **What:** one shared token, or several, for all targets (the `instance` model). A request passes when its token matches any entry.
- **Values:** a token, or a comma-separated list: `tok-a,tok-b`. Whitespace is trimmed, empty items and duplicates are dropped. A token cannot contain a comma.
- **Default:** unset (this model off).
- **Change it when:** always, on anything reachable beyond a trusted network. To rotate: add the new token, redeploy, move clients, remove the old one, redeploy.
- **Example:** `PROXY_AUTH_TOKEN=tok-a,tok-b` (real tokens: 64 hex characters each)
- **If wrong:** set together with `PROXY_AUTH_HOST_MAP` fails startup; set in route mode fails startup. Capacity: one env string is capped at 128 KiB on Linux and a Kubernetes Secret at 1 MiB, about two thousand 64-character tokens; each request costs one SHA-256 plus one comparison per token.

### `PROXY_AUTH_HOST_MAP`

- **What:** a different token per target host (the `per_host` model). A client can only use the token that belongs to the host it targets.
- **Values:** a one-line JSON object, host to token. Keys are bare target hosts: lowercased, port ignored, exact match, no suffixes.
- **Default:** unset.
- **Change it when:** different clients or teams must not be able to use each other's targets.
- **Example:** `PROXY_AUTH_HOST_MAP={"api.example.com":"tok-a","files.example.com":"tok-b"}`
- **If wrong:** invalid JSON or a map with no usable entry fails startup; both token variables set, or either one in route mode, fails startup. A target host missing from the map is refused as unauthorized, even if the allow list admits it.

### `AUTH_ENABLED`, `AUTH_MODE` (removed)

- **What:** former switches. They no longer do anything and their values are never read.
- **If set:** startup logs a warning that the variable is ignored and states the real auth state (`on (instance)`, `on (per_host)`, `off`). Delete them: auth follows the token variables only.
