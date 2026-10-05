.DEFAULT_GOAL := help

# Single source for the Go toolchain. Dockerfile, compose and CI read this.
GO_VERSION := $(shell tr -d '[:space:]' < version.txt)
export GO_VERSION

.PHONY: help run test lint check-go-version

help:
	@echo "run              go run ./cmd/lux-l7"
	@echo "test             fast unit tests (go test -race ./...)"
	@echo "lint             golangci-lint"
	@echo "check-go-version assert go.mod matches version.txt"
	@echo "docker-build     docker build (GO_VERSION from version.txt)"

run:
	go run ./cmd/lux-l7

test:
	go test -race ./...

lint:
	PATH="$$(go env GOPATH)/bin:$$PATH" golangci-lint run ./...

check-go-version:
	@mod="$$(sed -n 's/^go //p' go.mod | head -1 | tr -d '[:space:]')" && \
	if [ "$$mod" != "$(GO_VERSION)" ]; then \
		echo "go.mod ($$mod) != version.txt ($(GO_VERSION))" >&2; \
		exit 1; \
	fi

docker-build: check-go-version
	docker build \
		--build-arg GO_VERSION=$(GO_VERSION) \
		--build-arg BIN=lux-l7 \
		-t denisitpro/lux-l7:local .
