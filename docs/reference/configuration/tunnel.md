# CONNECT tunnel

> Status: living · Last verified: 2026-10-06 · [AI - claude-sonnet-5-5]

The optional second listener that relays raw TCP for `CONNECT host:port`. Part of the [configuration guide](../configuration.md). What the tunnel is and its limits: [`../capabilities.md`](../capabilities.md#connect-tunnel); the wire details: [`../protocol.md`](../protocol.md).

The tunnel exists only when `TUNNEL_ADDR` is set. Clients keep end-to-end TLS with the target, so the proxy never sees headers or bodies, only the target `host:port`. It needs a TCP-level (L4) exposure such as a LoadBalancer Service or TLS passthrough; an L7 ingress cannot carry `CONNECT`.

Startup rules for a tunnel (any failure exits at startup):

- header mode only (`TUNNEL_ADDR` with `PROXY_MODE=route` fails);
- it must be protected: a token variable (`PROXY_AUTH_TOKEN` or `PROXY_AUTH_HOST_MAP`), or at least one of `UPSTREAM_ALLOW_HOSTS` / `UPSTREAM_ALLOW_SUFFIXES`. Otherwise it would be an open relay and startup fails. `UPSTREAM_DENY_HOSTS` alone does not count;
- host policy, the SSRF guard and the token check are shared with header mode (see [`upstream-policy.md`](upstream-policy.md) and [`modes-and-auth.md`](modes-and-auth.md)); the port list is its own (`TUNNEL_ALLOW_PORTS`);
- `HTTP_READ_HEADER_TIMEOUT` and `UPSTREAM_TIMEOUT` apply to it too ([`listener.md`](listener.md)).

## Variables

### `TUNNEL_ADDR`

- **What:** the tunnel listen address; empty means no tunnel.
- **Values:** `host:port`: `:8443`, `0.0.0.0:8443`.
- **Default:** empty (off).
- **Change it when:** clients need to carry TLS to a target the proxy must not terminate (exchange APIs with signed requests, WebSocket streams).
- **Example:** `TUNNEL_ADDR=:8443`
- **If wrong:** route mode, or no auth and no allow list, fails startup (above). Like `HTTP_ADDR`, a malformed or busy address fails when listening starts. It must differ from `HTTP_ADDR` in practice (one socket per listener), and `METRICS_ADDR` must differ from it.

### `TUNNEL_ALLOW_PORTS`

- **What:** the target ports a `CONNECT` client may reach.
- **Values:** comma-separated integers, `1` to `65535`, at least one.
- **Default:** `443`.
- **Change it when:** the client must reach WebSocket or other TLS services on other ports, for example `443,8443,9443`.
- **Example:** `TUNNEL_ALLOW_PORTS=443,8443,9443`
- **If wrong:** an invalid port or an empty list fails startup, even when `TUNNEL_ADDR` is empty. A refused port at runtime is `403 forbidden`.

### `TUNNEL_IDLE_TIMEOUT`

- **What:** closes a tunnel after this long without bytes in either direction; traffic in either direction restarts the timer. There is no cap on total tunnel lifetime.
- **Values:** Go duration, greater than zero.
- **Default:** `10m`.
- **Change it when:** clients hold quiet connections open longer (raise it, or send keep-alive pings), or you want idle tunnels reclaimed sooner.
- **Example:** `TUNNEL_IDLE_TIMEOUT=30m`
- **If wrong:** zero, negative or unparsable fails startup.

### `TUNNEL_TLS_CERT_FILE`, `TUNNEL_TLS_KEY_FILE`

- **What:** a PEM certificate and key that turn the tunnel listener into a TLS listener (TLS 1.2 or higher). The client then sends its `Proxy-Authorization` credentials inside TLS instead of in clear text.
- **Values:** file paths, both set or both empty. The certificate file may contain the full chain.
- **Default:** empty (plain HTTP listener).
- **Change it when:** always when the tunnel crosses an untrusted network. Plain mode is for local tests.
- **Example:** `TUNNEL_TLS_CERT_FILE=/etc/lux-l7/tls.crt` and `TUNNEL_TLS_KEY_FILE=/etc/lux-l7/tls.key`
- **If wrong:** only one of the two set fails startup; an unreadable or mismatching pair fails startup. Without them the tunnel logs a startup warning that credentials travel in clear text. The files are re-read when either one's modification time changes (checked at the next handshake), so a renewed certificate needs no restart; if a reload fails, the previous certificate is kept and a warning is logged. If `TUNNEL_ADDR` is empty the pair is still required to be consistent but is otherwise unused.

## Client side

Clients send `Proxy-Authorization: Basic base64(user:token)`; the user part is ignored. Details: [`../protocol.md`](../protocol.md).
