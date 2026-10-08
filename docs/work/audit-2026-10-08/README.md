# Code audit, 2026-10-08

> Status: in-flight · Last verified: 2026-10-08 · [AI - claude-fable-5-1]
> Archive when: question 5 has a decision in #61 and the IPv6 and deny-suffix issues are closed or explicitly deferred.

## Scope

Full read of `cmd/lux-l7` and `internal/` (auth, certs, config, egress, front, listener, metrics, policy, proxy, relay, reqlog, route, tunnel) against `capabilities.md`, `protocol.md` and the configuration guide, plus a sweep for private identifiers before the repo goes public.

## Verified sound (no change)

- Check order (target, token, host policy, port, body, dial) in header, path and tunnel mode.
- Token comparison is constant-time with no early exit.
- The SSRF guard runs at dial time and covers IPv4-mapped, NAT64, 6to4 and v4-compatible embeddings.
- `HTTP_PROXY` is ignored by every upstream transport.
- Routing headers are stripped in every mode.
- Per-route fixed Host and SNI.
- Tunnel: the slot is released exactly once, a pipelined ClientHello is forwarded, half-close is handled, the idle deadline is extended both ways.
- Graceful shutdown of tunnels, with force-close at the deadline.
- Config fail-fast rules and socket clash detection.
- Metrics label cardinality is bounded by configuration.
- The request log never carries the client address, path, query or token.

## Fixed in this batch

| Issue | PR | Fix |
|---|---|---|
| #79 | #86 | stdlib `net/http` and `httputil` error lines (TLS handshake failures with the client address, body-copy failures with the upstream address) reached the JSON log at info; `ErrorLog` is now a discard logger on every listener and reverse proxy. |
| #87 | #90 | The route `tls_server_name` warning was written by the default text logger during config load; it now travels through `Config.Warnings` and the JSON handler. |
| #80 | #83 | Upstream transports kept 2 idle connections per host and dialed with a hard-coded 30 s; now 64 per host and the dial is bounded by `UPSTREAM_TIMEOUT` in every mode. |
| #84 | #89 | The certificate reloader stat-ed both PEM files on every TLS handshake under one mutex; now at most once per second, lock-free in between. |
| #81 | #85 | Docs drift: `forbidden` listed for route mode, `path` missing from the metrics mode label, hijacked WebSocket connections not drained on shutdown, exact names of the removed `AUTH_*` variables. |
| #82 | #88 | Tests used the registered domain `evil.net` as the denied-host fixture; now `denied.invalid`. |

## Owner decisions (2026-10-08)

1. CONNECT on the main listener in header and path mode is forwarded to the upstream as a CONNECT request (route mode answers 405 `method_not_allowed`). Decision: deferred; the CONNECT path on the main listener is not in use and untested yet, behaviour stays as is. Revisit when the tunnel is exercised.
2. A bracketed IPv6 literal without a port (`X-L7-Target-Host: [2001:db8::1]`) is refused as "invalid port", although the protocol doc says "IPv6 in brackets"; a policy test asserts the refusal. Decision: accept it, the port defaults from the scheme (see the issue titled `Bracketed IPv6 literal without a port is refused as "invalid port"`).
3. `UPSTREAM_TRUSTED_PROXIES` empty means every peer's inbound `X-Forwarded-*` is trusted when `UPSTREAM_FORWARD_CLIENT_IP=true`, so a direct client can spoof the chain. Decision: keep trusting every peer by default. A proxy forwards whatever it is given; deciding who may set `X-Forwarded-*` is the operator's job through `UPSTREAM_TRUSTED_PROXIES`.
4. `UPSTREAM_DENY_HOSTS` is exact-match only: a denied apex does not deny its subdomains. Decision: feature issue #93 (`UPSTREAM_DENY_SUFFIXES`), not scheduled.
5. #61 (route `rewrite_prefix` does not confine `..` paths) still waits for a decision: document that prefixes are routing, not an access boundary, or reject dot segments in route mode. Decision: pending, the owner asked for a clearer fork with a recommendation (tracked in #61).
6. Going public: the git history carries a personal author e-mail on most commits and commit messages that name the owner's other repositories; `CLAUDE.md` names the CI/CD reference repository (#78 covers that line); `claude-memory/` files quote private chat and session ids (#78 proposes deleting them). Decision: the repository stays private for now. Publication will happen as a new repository without history, after the tracked files are anonymised; no history rewrite here.

## Privacy sweep result

Tracked files contain no real hostnames, IPs, secrets, cluster names or personal paths beyond the items in question 6. Test fixtures use `example.com`, `.invalid` and documentation IP ranges.
