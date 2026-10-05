# lux-l7 — agent agreements

> Status: living · Last verified: 2026-10-05 · [AI - composer-2.5]

## Public repository — no private identifiers

This repo is **public**. Never commit or write in comments, docs, issues, or PR text:

- Real production hostnames, IPs, or URLs tied to the owner’s apps or employers
- Internal project codenames, company names, or neighbor-repo names used as examples
- API keys, tokens, or client secrets (use placeholders like `example.com`, `your-proxy.example`)

Use generic examples only (`api.example.com`, `proxy.example.net`). If a fact must stay private, it stays out of git.

## Scope

L7 reverse proxy: clients send the upstream target in-band; the service validates policy, optionally authenticates, and forwards to arbitrary allowed hosts (not a fixed ingress upstream list).

## Run (host)

```sh
cp .env.example test.env   # optional; gitignored
go run ./cmd/lux-l7
```

`test.env` is loaded when present and does not override variables already set in the environment.

## Docs

- Feature list and protocol: [`docs/feature.md`](docs/feature.md)
- Doc index: [`docs/README.md`](docs/README.md)

## Versioning

Release = git tag on `main`, plain semver without `v` (`0.2.0`), same as the sibling Go service. Pre-alpha line: bump the patch (`0.2.1`, `0.2.2`, …) per release until the owner declares the alpha ready. A tag push publishes the image via `.github/workflows/docker-release.yml`.

## Hard rules

- Do not run `kubectl apply` or cluster mutations unless the owner explicitly approves in chat.
- Git flow is the agent's job end to end, no asking: work on a `<issue>-<desc>` branch, commit, push, open the PR (`Closes #N`), merge it yourself (`gh pr merge --merge --delete-branch`), switch the local tree back to an up-to-date `main`, then confirm the `ci` run on `main` is green (CI runs only on push to `main`, not on PRs; a red run is fixed right away in a new branch). This overrides the global "only the owner merges" rule for this repo (owner's order, 2026-10-05).
- Never merge unreviewed subagent/Cursor output: before merging, the orchestrator reads the diff and runs `make test` / `make lint` locally; that is the pre-merge gate.
