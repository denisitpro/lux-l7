# syntax=docker/dockerfile:1.7
# Multi-stage image for lux-l7.
# GO_VERSION must match version.txt (Makefile and CI pass it).
#   docker build --build-arg GO_VERSION=$(tr -d '[:space:]' < version.txt) \
#     --build-arg BIN=lux-l7 -t denisitpro/lux-l7:local .

ARG GO_VERSION
FROM golang:${GO_VERSION}-trixie AS builder

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .

ARG BIN=lux-l7
ARG BUILD_ID=dev
ARG TARGETOS=linux
ARG TARGETARCH

RUN test -n "$BIN" && test -d "./cmd/${BIN}"
RUN if [ -z "$TARGETARCH" ]; then TARGETARCH="$(go env GOARCH)"; fi && \
    CGO_ENABLED=0 GOOS="$TARGETOS" GOARCH="$TARGETARCH" \
    go build -trimpath -ldflags="-s -w -X github.com/denisitpro/lux-l7/internal/version.Build=${BUILD_ID}" -o /out/service "./cmd/${BIN}"

FROM gcr.io/distroless/static-debian13:nonroot AS final

COPY --from=builder /out/service /service

USER nonroot:nonroot

ENTRYPOINT ["/service"]
