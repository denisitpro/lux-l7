# lux-l7 — agent agreements

> Status: living · Last verified: 2026-10-06 · [AI - claude-sonnet-5-5]

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

- What lux-l7 can do: [`docs/reference/capabilities.md`](docs/reference/capabilities.md)
- Client protocol: [`docs/reference/protocol.md`](docs/reference/protocol.md); env variables (configuration guide): [`docs/reference/configuration.md`](docs/reference/configuration.md)
- Doc index: [`docs/README.md`](docs/README.md)

## Versioning

Release = git tag on `main`, plain semver without `v` (`0.2.0`), same as the sibling Go service. Pre-alpha line: bump the patch (`0.2.1`, `0.2.2`, …) per release until the owner declares the alpha ready. A tag push publishes the image via `.github/workflows/docker-release.yml`.

When the agent finishes a batch of tasks and merges them, it tags the next patch release itself (`0.2.X` → `0.2.X+1`, annotated signed tag: `git tag -m <ver> <ver>`), pushes the tag and checks the docker-build run is green. Minor/major bumps only on the owner's word.

## CI/CD reference

rate-hub is the CI/CD reference for every backend repo (#26): `.github/workflows/`, `.github/actions/go-toolchain`, `Dockerfile`, Makefile CI targets, `.golangci.yml`, Dependabot are copied from it with minimal edits; a deviation is a bug. Intentional deviations: Debian 13 base images (#27), plain-semver tags, single binary, no catalog/oasdiff checks.

CI runs only on push to `main` (no `pull_request` trigger, on purpose: limited GitHub minutes, a red `main` is the tripwire for a skipped local check). Before reporting any work as done, run `make check` (`check-go-version` + `lint` + `test` + `vuln`) and quote the result; for a change to the `Dockerfile`, its build args, `.github/` or the `Makefile`, also run `make docker`. A red run on `main` is an agent-discipline defect, not "CI doing its job".

## Hard rules

- A PR that adds or changes a capability updates `docs/reference/capabilities.md` in the same PR.
- A PR that adds or changes an env parameter updates the configuration guide (`docs/reference/configuration.md` and its topic page under `docs/reference/configuration/`) in the same PR.
- Do not run `kubectl apply` or cluster mutations unless the owner explicitly approves in chat.
- Git flow is the agent's job end to end, no asking: work on a `<issue>-<desc>` branch, commit, push, open the PR (`Closes #N`), merge it yourself (`gh pr merge --merge --delete-branch`), switch the local tree back to an up-to-date `main`, then confirm the `ci` run on `main` is green (CI runs only on push to `main`, not on PRs; a red run is fixed right away in a new branch). This overrides the global "only the owner merges" rule for this repo (owner's order, 2026-10-05).
- Never merge unreviewed subagent/Cursor output: before merging, the orchestrator reads the diff and runs `make test` / `make lint` locally; that is the pre-merge gate.
