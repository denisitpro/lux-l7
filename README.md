# lux-l7

L7 egress proxy: clients reach one stable endpoint and name the real upstream per request (header mode), tunnel raw TLS through `CONNECT`, or use a static route table. Optional token auth, SSRF guard, upstream allow/deny lists. Stateless, one static binary, container image.

## Integrating a client

Writing a client or an agent that calls this proxy? Everything you need is in one file: [`docs/reference/protocol.md`](docs/reference/protocol.md) (request headers per mode, error codes, how to tell a proxy error from an upstream one, compatibility rules).

## Quick start

No environment is required:

```sh
go run ./cmd/lux-l7
```

It listens on `:8080` in header mode with auth off, so do not expose it publicly as is. Optional settings go in the environment or in a `test.env` copied from [`.env.example`](.env.example).

```sh
curl -H 'X-L7-Target-Host: example.com' http://localhost:8080/
```

Probes: `GET /healthz`, `GET /readyz`, `GET /version`.

## Documentation

- [`docs/reference/capabilities.md`](docs/reference/capabilities.md): what it does
- [`docs/reference/protocol.md`](docs/reference/protocol.md): client contract and error codes
- [`docs/reference/configuration.md`](docs/reference/configuration.md): configuration guide, every env variable (topic pages in `docs/reference/configuration/`)
- [`docs/README.md`](docs/README.md): index and fact owners
- [`CLAUDE.md`](CLAUDE.md): agent rules
