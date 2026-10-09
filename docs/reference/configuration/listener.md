# Listener, shutdown and logging

> Status: living · Last verified: 2026-10-08 · [AI - claude-sonnet-5-5]

How the main HTTP listener behaves, how the process stops, and how it logs. Part of the [configuration guide](../configuration.md); the common value rules (durations, booleans) are there.

The main listener speaks plain HTTP/1.x by default: end TLS at an ingress or load balancer in front. Without an ingress it can terminate TLS itself (`HTTP_TLS_*`, HTTP/2 through ALPN) or accept cleartext HTTP/2 (`HTTP_H2C`). The tunnel listener has its own TLS, see [`tunnel.md`](tunnel.md).

## Listener

### `HTTP_ADDR`

- **What:** the address the main listener binds to, in `host:port` form.
- **Values:** `:8080` (all interfaces), `127.0.0.1:8080` (this host only), `0.0.0.0:9000`.
- **Default:** `:8080`.
- **Change it when:** the port is taken, or you want to bind to loopback only behind a local sidecar.
- **Example:** `HTTP_ADDR=127.0.0.1:8080`
- **Privileged port (below 1024):** with `network_mode: host`, use the `-host` image tag (for example `latest-host`). The `-host` variant cannot start with `cap_drop: ALL` unless `cap_add: NET_BIND_SERVICE` (the default image is unaffected). The same applies to `TUNNEL_ADDR` and `METRICS_ADDR`.
- **If wrong:** the value itself is not checked while loading config: a malformed or busy address makes the process fail when it starts listening (a permission error names the cause: privileged port, use the `-host` image or a port >= 1024). Two listeners on one socket fail startup: `TUNNEL_ADDR` or `METRICS_ADDR` clashing with it is rejected (the clash rule is in [`../configuration.md`](../configuration.md#combinations-that-fail-startup); see also [`tunnel.md`](tunnel.md) and [`metrics.md`](metrics.md)).

### `HTTP_TLS_CERT_FILE`, `HTTP_TLS_KEY_FILE`

- **What:** a PEM certificate and key that turn the main listener into a TLS listener (TLS 1.2 or higher). ALPN offers `h2` and `http/1.1`, so HTTP/2 clients get HTTP/2 and others fall back to HTTP/1.1. Works in every proxy mode; `/healthz`, `/readyz` and `/version` are served over TLS too.
- **Values:** file paths, both set or both empty. The certificate file may contain the full chain. A self-signed certificate works (clients must trust it).
- **Default:** empty (plain HTTP/1.1 listener, as before).
- **Change it when:** the proxy runs without an ingress or load balancer that ends TLS, i.e. on its own published port.
- **Example:** `HTTP_TLS_CERT_FILE=/etc/lux-l7/tls.crt` and `HTTP_TLS_KEY_FILE=/etc/lux-l7/tls.key`
- **If wrong:** only one of the two set fails startup; an unreadable or mismatching pair fails startup. The files are re-read when either one's modification time changes (checked at the next handshake, at most once per second), so a renewed certificate needs no restart; if a reload fails, the previous certificate is kept and a warning is logged. Each successful load logs `http tls certificate loaded` (or `reloaded`) with `key_type`, `key_bits` or `curve`, `subject_cn`, `sans` and `not_after`; no key material. Same behaviour as `TUNNEL_TLS_*`; the tunnel pair is separate and can differ.
- **WebSocket:** `net/http` does not serve WebSocket over HTTP/2 (RFC 8441 extended `CONNECT`), so WebSocket clients must use HTTP/1.1 (the usual `Upgrade` handshake over the same TLS port; browsers and common libraries do this by default). Requests that arrive over HTTP/2 are proxied to the upstream as before; SSE and streaming work over both.
- **Deployment without ingress:** publish the port directly, for example in Docker Compose:

```yaml
services:
  lux-l7:
    image: your-registry.example/lux-l7:latest
    ports:
      - "443:8443"
    environment:
      HTTP_ADDR: ":8443"
      HTTP_TLS_CERT_FILE: /certs/tls.crt
      HTTP_TLS_KEY_FILE: /certs/tls.key
    volumes:
      - ./certs:/certs:ro
```

  Built-in certificate issuance (ACME) and HTTP/3 are not part of this option.

### `HTTP_H2C`

- **What:** accepts cleartext HTTP/2 with prior knowledge (the client opens the connection speaking HTTP/2, no upgrade) on the main listener, next to HTTP/1.1 on the same port.
- **Values:** boolean, `true` or `false`.
- **Default:** `false`: a prior-knowledge HTTP/2 client is refused, HTTP/1.1 is unchanged.
- **Change it when:** a trusted hop in front (a sidecar, a gRPC-style client or an internal load balancer) speaks h2c to the proxy over a private network. Cleartext: never on an untrusted network.
- **Example:** `HTTP_H2C=true`
- **If wrong:** an unparsable value fails startup. Set together with `HTTP_TLS_CERT_FILE` it is ignored with a startup warning (TLS already offers HTTP/2, and the TLS port carries no cleartext). The `Upgrade: h2c` handshake is not supported, only prior knowledge. WebSocket over HTTP/2 is not supported (see above).

### `HTTP_READ_HEADER_TIMEOUT`

- **What:** how long a client may take to send the request headers. Protects against slow-header connections. The tunnel listener uses the same value for reading the `CONNECT` request.
- **Values:** Go duration greater than zero (`5s`, `500ms`).
- **Default:** `5s`.
- **Change it when:** clients on very slow links legitimately need longer; keep it short on a public endpoint.
- **Example:** `HTTP_READ_HEADER_TIMEOUT=10s`
- **If wrong:** zero or negative fails startup (`must be > 0`), because Go's HTTP server would apply no limit and the protection would be gone; unparsable fails startup.

### `HTTP_READ_TIMEOUT`

- **What:** a cap on reading the whole request, body included. Off by default on purpose.
- **Values:** Go duration; `0` means off.
- **Default:** `0` (off).
- **Change it when:** almost never. A non-zero value cuts slow uploads and long-lived streams, WebSocket included, mid-flight.
- **Example:** `HTTP_READ_TIMEOUT=0`
- **If wrong:** a negative value fails startup (`must be >= 0`); an unparsable value fails startup.

### `HTTP_WRITE_TIMEOUT`

- **What:** a cap on writing the whole response. Off by default on purpose.
- **Values:** Go duration; `0` means off.
- **Default:** `0` (off).
- **Change it when:** almost never. A non-zero value cuts SSE, long downloads and WebSocket after upgrade, however active they are. Idle protection comes from `HTTP_IDLE_TIMEOUT` and `UPSTREAM_TIMEOUT` instead.
- **Example:** `HTTP_WRITE_TIMEOUT=0`
- **If wrong:** a negative value fails startup (`must be >= 0`); an unparsable value fails startup.

### `HTTP_IDLE_TIMEOUT`

- **What:** how long an idle keep-alive client connection stays open between requests, on the main listener. It also applies to the tunnel listener, as a backstop for connections waiting for a next request (a refused `CONNECT` closes its connection anyway).
- **Values:** Go duration greater than zero.
- **Default:** `120s`.
- **Change it when:** a load balancer in front closes idle connections sooner or later than 120 seconds and you want the proxy to match it.
- **Example:** `HTTP_IDLE_TIMEOUT=60s`
- **If wrong:** zero or negative fails startup (`must be > 0`), because the keep-alive time would be unbounded; unparsable fails startup.

## Shutdown

### `SHUTDOWN_TIMEOUT`

- **What:** on SIGINT or SIGTERM the main listener and the tunnel stop accepting new connections and wait this long for in-flight work. Tunnels still open at the deadline are closed.
- **Values:** Go duration, greater than zero.
- **Default:** `15s`.
- **Change it when:** requests run longer than 15 seconds and you want them to finish on deploy. Keep it below the orchestrator's kill grace period (for Kubernetes, `terminationGracePeriodSeconds`).
- **Example:** `SHUTDOWN_TIMEOUT=30s`
- **If wrong:** zero or negative fails startup (`must be > 0`); unparsable fails startup.

## Logging

### `LOG_LEVEL`

- **What:** the minimum level written to the JSON log on stdout. It is the only logging setting: the proxy mode decides what each level emits.
- **Values:** `debug`, `info`, `warn` (or `warning`), `error`; case-insensitive.
- **Default:** `info`.
- **Change it when:** `debug` for troubleshooting (one line per request, which in header mode, path mode and tunnel includes the target host); `warn` to quiet a busy deployment. Keep `info` in production: in header mode, path mode and tunnel it logs no request data at all, in route mode one summary line per route per minute.
- **Example:** `LOG_LEVEL=warn`
- **If wrong:** any other value fails startup (`unknown LOG_LEVEL`).

Startup configuration warnings (empty allow list, auth off, ignored variables, a tunnel without TLS) are logged at `warn` once the logger is set up, so `LOG_LEVEL=error` hides them. What is and is not logged per request: [`../capabilities.md`](../capabilities.md#json-logs).
