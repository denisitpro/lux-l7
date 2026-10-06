# Listener, shutdown and logging

> Status: living · Last verified: 2026-10-06 · [AI - claude-sonnet-5-5]

How the main HTTP listener behaves, how the process stops, and how it logs. Part of the [configuration guide](../configuration.md); the common value rules (durations, booleans) are there.

The main listener speaks plain HTTP/1.x. There is no TLS option on it: end TLS at an ingress or load balancer in front. (The tunnel listener has its own TLS, see [`tunnel.md`](tunnel.md).)

## Listener

### `HTTP_ADDR`

- **What:** the address the main listener binds to, in `host:port` form.
- **Values:** `:8080` (all interfaces), `127.0.0.1:8080` (this host only), `0.0.0.0:9000`.
- **Default:** `:8080`.
- **Change it when:** the port is taken, or you want to bind to loopback only behind a local sidecar.
- **Example:** `HTTP_ADDR=127.0.0.1:8080`
- **If wrong:** the value is not checked while loading config. A malformed or busy address makes the process fail when it starts listening. If `METRICS_ADDR` equals it, the metrics address is rejected at startup (see [`metrics.md`](metrics.md)).

### `HTTP_READ_HEADER_TIMEOUT`

- **What:** how long a client may take to send the request headers. Protects against slow-header connections. The tunnel listener uses the same value for reading the `CONNECT` request.
- **Values:** Go duration (`5s`, `500ms`).
- **Default:** `5s`.
- **Change it when:** clients on very slow links legitimately need longer; keep it short on a public endpoint.
- **Example:** `HTTP_READ_HEADER_TIMEOUT=10s`
- **If wrong:** an unparsable value fails startup. A zero or negative value is not rejected, and Go's HTTP server then applies no limit, which removes the protection, so do not set it.

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

- **What:** how long an idle keep-alive client connection stays open between requests, on the main listener.
- **Values:** Go duration.
- **Default:** `120s`.
- **Change it when:** a load balancer in front closes idle connections sooner or later than 120 seconds and you want the proxy to match it.
- **Example:** `HTTP_IDLE_TIMEOUT=60s`
- **If wrong:** an unparsable value fails startup. Zero and negative values are not rejected; do not set them.

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

- **What:** the minimum level written to the JSON log on stdout.
- **Values:** `debug`, `info`, `warn` (or `warning`), `error`; case-insensitive.
- **Default:** `info`.
- **Change it when:** `debug` for troubleshooting (the tunnel then logs target hosts); `warn` to quiet a busy deployment.
- **Example:** `LOG_LEVEL=warn`
- **If wrong:** any other value fails startup (`unknown LOG_LEVEL`).

Startup configuration warnings (empty allow list, auth off, ignored variables, a tunnel without TLS) are logged at `warn` once the logger is set up, so `LOG_LEVEL=error` hides them. What is and is not logged per request: [`../capabilities.md`](../capabilities.md#json-logs).
