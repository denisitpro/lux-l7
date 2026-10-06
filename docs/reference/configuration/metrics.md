# Metrics

> Status: living · Last verified: 2026-10-06 · [AI - claude-sonnet-5-5]

Prometheus metrics on their own listener. Part of the [configuration guide](../configuration.md). The series, labels and the cardinality bound are owned by [`../capabilities.md`](../capabilities.md#prometheus-metrics).

Metrics are off by default and nothing is recorded while off. `/metrics` is served on a separate listener, never on the main or tunnel port, and has no authentication: scrape it from a private network and never publish it through a public ingress.

`METRICS_LOCATION` and `METRICS_INGRESS_HOSTS` only matter when `METRICS_ADDR` is set; set without it, startup logs one warning and they are ignored.

## Variables

### `METRICS_ADDR`

- **What:** the bind address of the metrics listener. Setting it turns metrics on.
- **Values:** `host:port` with a numeric port: `:9090` (all interfaces, typical in Kubernetes where Prometheus scrapes the pod IP), `127.0.0.1:9090` (this host only, bare metal or host networking).
- **Default:** empty (off).
- **Change it when:** you run Prometheus.
- **Example:** `METRICS_ADDR=:9090`
- **If wrong:** a missing port, a non-numeric port, a value equal to `HTTP_ADDR`, or equal to `TUNNEL_ADDR` fails startup (`METRICS_ADDR: ...`). A busy port fails when listening starts.

### `METRICS_LOCATION`

- **What:** a constant `location` label added to every `lux_l7_` series, so one dashboard can tell instances in different places apart. Go and process series do not get it.
- **Values:** 1 to 32 characters: letters, digits, `.`, `_`, `-`, starting with a letter or digit.
- **Default:** empty (no label).
- **Change it when:** you run several instances and want the place in the series. A Prometheus target label does the same job and also covers the Go and process series.
- **Example:** `METRICS_LOCATION=eu-1`
- **If wrong:** an invalid value fails startup. Set without `METRICS_ADDR` it is ignored with a warning.

### `METRICS_INGRESS_HOSTS`

- **What:** in header mode, the front hostnames allowed to appear as the `ingress_host` label (the `Host` header clients used to reach the proxy). Any other `Host` is counted as `other`, which keeps the number of series bounded no matter what clients send.
- **Values:** comma-separated bare hostnames, lowercased automatically, no ports: `front.example.com,front2.example.com`.
- **Default:** empty (every request is `other`).
- **Change it when:** several front hostnames reach one header-mode instance and you want per-front numbers. Each entry adds label values, so list only the hosts you really use.
- **Example:** `METRICS_INGRESS_HOSTS=front.example.com`
- **If wrong:** an entry with a port or invalid characters fails startup. In route mode it is ignored with a warning, because labels come from the `host` field of `ROUTES`. Without `METRICS_ADDR` it is ignored with a warning. The tunnel always reports `other`.
