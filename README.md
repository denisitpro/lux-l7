# lux-l7

L7 reverse proxy for API traffic: clients hit one reachable endpoint and specify the real upstream host per request (header-based routing). Built for stateless Kubernetes deployment and optional token auth.

## Status

Early scaffold — health, config, and L7 forwarding. See [`docs/feature.md`](docs/feature.md).

## Run locally

```sh
cp .env.example test.env   # optional
go run ./cmd/lux-l7
```

Default listen `:8080`. See [`docs/feature.md`](docs/feature.md) for the planned protocol and env variables.

## Health

- `GET /healthz` — liveness
- `GET /readyz` — readiness
- `GET /version` — build version as JSON (`{"version":"..."}`), no auth

## Documentation

- [`docs/feature.md`](docs/feature.md) — capabilities and roadmap
- [`CLAUDE.md`](CLAUDE.md) — agent rules (public repo: no private names in tree)
