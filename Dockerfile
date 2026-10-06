# syntax=docker/dockerfile:1.19
# check=skip=InvalidDefaultArgInFrom
# (A no-default ARG in FROM is intentional — a missing GO_VERSION must fail the build, as in the reference.)
# Multi-stage image for lux-l7.
#   builder — module download + static Go binary
#   final   — distroless static, non-root; binary at /service, build metadata in /app/version.txt
#
# Build via BIN (single binary):
#   make docker            (reads version.txt)
#   docker build --build-arg GO_VERSION=$(tr -d '[:space:]' < version.txt) \
#     --build-arg BIN=lux-l7 --build-arg BUILD_ID=local \
#     --build-arg CI_COMMIT_SHORT_SHA=$(git rev-parse --short=7 HEAD) \
#     --build-arg CI_COMMIT_REF_NAME=$(git rev-parse --abbrev-ref HEAD) \
#     --build-arg CI_COMMIT_TIMESTAMP=$(date -u +%Y-%m-%dT%H:%M:%SZ) -t lux-l7:local .

# Build arguments — no defaults on purpose.
# A missing value from CI must fail the build loudly, not produce a broken image.
# GO_VERSION must match version.txt (Makefile and CI pass it).
ARG GO_VERSION
# The builder base is deliberately NOT pinned by digest: its tag is derived from
# GO_VERSION, and a digest would pin one version and defeat the argument.
FROM golang:${GO_VERSION}-trixie AS builder

# Global ARGs are not inherited into a stage; re-declare and validate.
ARG GO_VERSION
ARG BIN
ARG BUILD_ID
ARG TARGETOS=linux
ARG TARGETARCH

RUN test -n "$GO_VERSION" || { echo "build-arg GO_VERSION is required" >&2; exit 1; }
RUN test -n "$BUILD_ID" || { echo "build-arg BUILD_ID is required" >&2; exit 1; }
RUN test -n "$BIN" || { echo "build-arg BIN is required" >&2; exit 1; }

WORKDIR /src

COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download

COPY . .

# Build metadata, declared after the source copy so a new value never invalidates
# the module-download or COPY layers. No defaults, validated loudly.
ARG CI_COMMIT_SHORT_SHA
ARG CI_COMMIT_REF_NAME
ARG CI_COMMIT_TIMESTAMP
RUN test -n "$CI_COMMIT_SHORT_SHA" || { echo "build-arg CI_COMMIT_SHORT_SHA is required" >&2; exit 1; }
RUN test -n "$CI_COMMIT_REF_NAME" || { echo "build-arg CI_COMMIT_REF_NAME is required" >&2; exit 1; }
RUN test -n "$CI_COMMIT_TIMESTAMP" || { echo "build-arg CI_COMMIT_TIMESTAMP is required" >&2; exit 1; }

RUN test -d "./cmd/${BIN}" || { echo "unknown BIN: ${BIN}" >&2; exit 1; }
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    if [ -z "$TARGETARCH" ]; then TARGETARCH="$(go env GOARCH)"; fi && \
    V=github.com/denisitpro/lux-l7/internal/version && \
    CGO_ENABLED=0 GOOS="$TARGETOS" GOARCH="$TARGETARCH" \
    go build -trimpath -ldflags="-s -w -X ${V}.Build=${BUILD_ID} -X ${V}.Commit=${CI_COMMIT_SHORT_SHA} -X ${V}.Ref=${CI_COMMIT_REF_NAME} -X ${V}.BuiltAt=${CI_COMMIT_TIMESTAMP}" -o /out/service "./cmd/${BIN}"
# distroless/static has no dynamic loader: a dynamically linked binary dies with
# "exec /service: no such file or directory" (reference incident). Fail the build instead.
RUN if ldd /out/service >/dev/null 2>&1; then echo "/out/service is dynamically linked; CGO_ENABLED=0 did not reach go build" >&2; exit 1; fi

# Build metadata in the reference format (one "key: value" per line). Written here, not in
# the final stage: distroless has no shell, so printf only exists in the builder.
RUN printf 'version: %s\ntimestamp: %s\ncommitHash: %s\nbranch: %s\ngoVersion: %s\nbinary: %s\n' \
    "${BUILD_ID}" "${CI_COMMIT_TIMESTAMP}" "${CI_COMMIT_SHORT_SHA}" "${CI_COMMIT_REF_NAME}" "${GO_VERSION}" "${BIN}" > /out/version.txt

# ---------------------------------------------------------------------------

# Not pinned by digest, same as the reference. The image is tiny (no libc,
# no shell) and we only build on tags, so freshness is bounded by release cadence.
FROM gcr.io/distroless/static-debian13:nonroot AS final

WORKDIR /app
COPY --from=builder /out/service /service
COPY --from=builder /out/version.txt /app/version.txt

USER nonroot:nonroot

ENTRYPOINT ["/service"]
