# Upstream limits and policy

> Status: living · Last verified: 2026-10-06 · [AI - claude-sonnet-5-5]

What a client may reach in header and path mode, and the limits on upstream calls. Part of the [configuration guide](../configuration.md). The mechanisms behind these variables: [`../capabilities.md`](../capabilities.md#upstream-policy); error codes: [`../protocol.md`](../protocol.md).

In route mode the three host lists are ignored (startup warning), and `UPSTREAM_ALLOW_HTTP`, `UPSTREAM_ALLOW_PRIVATE`, `UPSTREAM_ALLOW_PORTS` and `UPSTREAM_DEFAULT_SCHEME` have no effect; `UPSTREAM_TIMEOUT` and `UPSTREAM_MAX_BYTES` still apply. The ports and boolean variables, `UPSTREAM_DEFAULT_SCHEME` and `ROUTE_MAX_HOPS` are nevertheless parsed in every mode, so a typo in them fails startup even where they have no effect. This is deliberate (fail fast beats a silently ignored typo), and the error says so: `... (checked in every PROXY_MODE, even where the variable has no effect)`. The same holds for `TUNNEL_ALLOW_PORTS`.

## Who may be reached

### `UPSTREAM_ALLOW_HOSTS`

- **What:** exact hostnames clients may target. When any allow list is non-empty, only matching names are admitted. The tunnel uses the same lists.
- **Values:** comma-separated bare hostnames, lowercased automatically: `api.example.com,files.example.com`. No ports. One trailing dot is dropped.
- **Default:** empty.
- **Change it when:** always in production: list exactly what clients need. It is also one of the two things that make a tunnel without auth acceptable ([`tunnel.md`](tunnel.md)).
- **Example:** `UPSTREAM_ALLOW_HOSTS=api.example.com`
- **If wrong:** an entry with a port (`api.example.com:443`) fails startup. A target not on the list is `403 forbidden`. With both allow lists empty every public host is admitted and header and path mode log a warning.

### `UPSTREAM_ALLOW_SUFFIXES`

- **What:** domain suffixes clients may target. A suffix admits the domain itself and all its subdomains.
- **Values:** comma-separated: `example.com` and `.example.com` behave the same and admit `example.com` and `a.example.com`, not `notexample.com`.
- **Default:** empty.
- **Change it when:** a whole domain is trusted and subdomains change often.
- **Example:** `UPSTREAM_ALLOW_SUFFIXES=.example.com,.example.net`
- **If wrong:** same as `UPSTREAM_ALLOW_HOSTS`: a port in an entry fails startup.

### `UPSTREAM_DENY_HOSTS`

- **What:** exact hostnames that are always refused. Deny wins over both allow lists.
- **Values:** comma-separated bare hostnames, exact match only (no suffixes).
- **Default:** empty.
- **Change it when:** you allow a wide suffix but must carve out one host, for example an internal name under it.
- **Example:** `UPSTREAM_DENY_HOSTS=admin.example.com`
- **If wrong:** a port in an entry fails startup. Deny alone does not restrict anything else: it does not count as an allow list, so the empty-allow-list warning stays, and it does not make an unauthenticated tunnel acceptable.

## Network safety

### `UPSTREAM_ALLOW_PRIVATE`

- **What:** the SSRF guard checks the resolved IP of every upstream at dial time and refuses loopback, private (RFC 1918), CGNAT, link-local (including cloud metadata), multicast and other non-public addresses. `true` turns the guard off, for header mode, path mode and the tunnel.
- **Values:** boolean.
- **Default:** `false` (guard on).
- **Change it when:** only for local tests against `127.0.0.1` upstreams. Never in production.
- **Example:** `UPSTREAM_ALLOW_PRIVATE=true`
- **If wrong:** an unparsable boolean fails startup. `true` logs a startup warning whenever the guard is off on a listener that uses it: in header or path mode, or with `TUNNEL_ADDR` set. In route mode without a tunnel it has no effect and no warning. A blocked address at runtime is `403 forbidden`.

### `UPSTREAM_ALLOW_PORTS`

- **What:** the target ports header and path mode may reach. An implicit port counts as 443 for https and 80 for http.
- **Values:** comma-separated integers, `1` to `65535`, at least one.
- **Default:** `80,443,8443,9443`.
- **Change it when:** an upstream listens on another port, or to narrow it to `443`.
- **Example:** `UPSTREAM_ALLOW_PORTS=443`
- **If wrong:** an invalid port, `0`, an out-of-range number, or an empty list fails startup. A refused port at runtime is `403 forbidden`. The tunnel has its own list, `TUNNEL_ALLOW_PORTS`.

## Scheme

### `UPSTREAM_DEFAULT_SCHEME`

- **What:** the scheme used when the client sends no `X-L7-Target-Scheme`.
- **Values:** `https` or `http`; case-insensitive.
- **Default:** `https`.
- **Change it when:** almost never; `http` only for local tests, together with `UPSTREAM_ALLOW_HTTP=true`.
- **Example:** `UPSTREAM_DEFAULT_SCHEME=https`
- **If wrong:** any other value fails startup, in every mode. `http` without `UPSTREAM_ALLOW_HTTP=true` fails startup in header and path mode.

### `UPSTREAM_ALLOW_HTTP`

- **What:** permits plain-http upstreams in header and path mode (path mode has no client scheme override, so only `UPSTREAM_DEFAULT_SCHEME=http` selects http there). Off by default so a client cannot downgrade the hop to the upstream: a request asking for `http` is refused with `400 bad_target`.
- **Values:** boolean.
- **Default:** `false`.
- **Change it when:** an internal upstream only speaks http and you accept an unencrypted hop. Pair it with an allow list.
- **Example:** `UPSTREAM_ALLOW_HTTP=true`
- **If wrong:** an unparsable boolean fails startup.

## Client IP forwarding

Header and path mode. In route mode both variables are ignored (startup warning): route mode always sets `X-Forwarded-*` itself. Both are parsed in every mode, so a typo fails startup.

### `UPSTREAM_FORWARD_CLIENT_IP`

- **What:** sends `X-Forwarded-For`, `X-Forwarded-Host` and `X-Forwarded-Proto` to the upstream. Off: none of the three reaches the upstream, including client-sent ones. On: the connecting address is appended to the inbound `X-Forwarded-For` chain; `X-Forwarded-Host` is the incoming `Host` and `X-Forwarded-Proto` is `https` only when the client connection used TLS. Inbound `X-Forwarded-*` from the client is trusted by default (the way ingress controllers do it): the inbound chain is kept and extended, an inbound `X-Forwarded-Host` and a valid inbound `X-Forwarded-Proto` (`http` or `https`) replace the proxy's own values.
- **Values:** boolean.
- **Default:** `false` (the proxy stays opaque).
- **Change it when:** the upstream needs the real client address (rate limits, audit, geo).
- **Example:** `UPSTREAM_FORWARD_CLIENT_IP=true`
- **If wrong:** an unparsable boolean fails startup. With the default trust, a client that talks to the proxy directly can forge `X-Forwarded-For` entries (the proxy's appended address is still the real one); set `UPSTREAM_TRUSTED_PROXIES` if the upstream reads the chain.

### `UPSTREAM_TRUSTED_PROXIES`

- **What:** restricts who may supply inbound `X-Forwarded-*`. When set, inbound values are honoured only if the connecting address is in the list; otherwise they are dropped and the chain starts at the connecting address. The connecting address is the direct TCP peer, so behind a load balancer list the load balancer's address range.
- **Values:** comma-separated IPs and CIDRs, IPv4 and IPv6 (`10.0.0.0/8,192.0.2.7,2001:db8::/32`). A bare IP is a single host.
- **Default:** empty (every peer is trusted).
- **Change it when:** clients can reach the proxy directly and the upstream trusts the `X-Forwarded-For` chain.
- **Example:** `UPSTREAM_TRUSTED_PROXIES=10.0.0.0/8,2001:db8::/32`
- **If wrong:** an entry that is not an IP or CIDR fails startup, naming the variable. Set without `UPSTREAM_FORWARD_CLIENT_IP=true` it logs a startup warning and does nothing.

## Limits

### `UPSTREAM_TIMEOUT`

- **What:** how long the proxy waits for the upstream to start answering (response headers), in every mode. For the tunnel it is the dial timeout. It does not limit a response body that is already streaming.
- **Values:** Go duration, greater than zero.
- **Default:** `30s`.
- **Change it when:** upstreams are slow to first byte (long-running API calls), or you want to fail fast.
- **Example:** `UPSTREAM_TIMEOUT=60s`
- **If wrong:** zero, negative or unparsable fails startup. A timeout at runtime is `504 upstream_timeout`.

### `UPSTREAM_MAX_BYTES`

- **What:** the largest request body accepted, in every mode.
- **Values:** an integer number of bytes, greater than zero (no `MB` suffixes).
- **Default:** `33554432` (32 MiB).
- **Change it when:** clients upload larger files, or you want a tighter cap.
- **Example:** `UPSTREAM_MAX_BYTES=104857600` (100 MiB)
- **If wrong:** zero, negative or non-numeric fails startup. An oversized body is `413 body_too_large`, also for a chunked body that grows past the limit while streaming.
