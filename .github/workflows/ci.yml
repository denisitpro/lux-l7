name: ci

# Free-minute budget: run checks only after merge to main (push), never on
# pull_request. A red main is the signal that an agent skipped `make check`.
on:
  push:
    branches:
      - main
  workflow_dispatch:

# One ref, one run. A newer push supersedes the previous run instead of
# queueing behind it — there is no point paying for a verdict on a commit
# that has already been superseded.
concurrency:
  group: ci-${{ github.workflow }}-${{ github.ref }}
  cancel-in-progress: true

permissions:
  contents: read

jobs:
  checks:
    runs-on: ubuntu-latest
    timeout-minutes: 20
    steps:
      - uses: actions/checkout@v7

      - name: Go toolchain from version.txt
        uses: ./.github/actions/go-toolchain

      - name: go build
        run: go build ./...

      - name: go test (race)
        run: go test -race ./...

      - uses: golangci/golangci-lint-action@v9
        with:
          version: v2.14.0
          # Caches removed on purpose (reference decision): builds run on free GitHub runners, releases
          # are rare so the hit rate is ~0 (GHA evicts entries unused for 7 days), and a
          # stale or poisoned cache is a risk for a release image. Do not re-add.
          skip-cache: true

      # Vulnerability audit (Go analogue of the reference's trivy audit job).
      - name: govulncheck
        run: make vuln
