# Documentation index

> Status: living · Last verified: 2026-10-06 · [AI - claude-sonnet-5-5]

Read `../CLAUDE.md` first, then the one document the question below points at.

## Where to start

| Question | Document |
|---|---|
| What can lux-l7 do, how is each thing enabled, what are its limits | [`reference/capabilities.md`](reference/capabilities.md) |
| What does a client send, and what error does it get | [`reference/protocol.md`](reference/protocol.md) |
| What does each env variable mean, what is its default | [`reference/configuration.md`](reference/configuration.md) |
| Why a custom service, why Go | [`reference/prior-art.md`](reference/prior-art.md) |
| How to run it locally | [`../README.md`](../README.md) |
| Agent and public-repo rules, release and merge flow | [`../CLAUDE.md`](../CLAUDE.md) (`AGENTS.md` is a symlink) |

## Fact owners

Each class of fact has one owner; everything else links.

| Class of fact | Owner |
|---|---|
| Capabilities: what, how enabled, limits, origin PR | `reference/capabilities.md` |
| Wire contract: headers, status codes, error codes, JSON error shape | `reference/protocol.md` |
| Env variables: names, defaults, validation, route table fields | `reference/configuration.md` (copy-paste defaults: [`../.env.example`](../.env.example)) |
| Prior art and the language decision | `reference/prior-art.md` |
| Roadmap and ticket status | [GitHub issues, `label:feature`](https://github.com/denisitpro/lux-l7/issues?q=is%3Aopen+label%3Afeature) |
| Released versions | git tags |
| Build, run, release and merge rules, hard prohibitions | `../CLAUDE.md` |

## Where a new doc goes

Durable fact about behaviour: `reference/`, 200 lines at most, with the freshness stamp, and a row in the table above. In-flight material: `work/` with an `Archive when:` field. A change to a capability, header, status code or env variable updates its owner in the same PR.
