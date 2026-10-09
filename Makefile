.DEFAULT_GOAL := help

# Single source for the Go toolchain. Dockerfile and CI read this.
GO_VERSION := $(shell tr -d '[:space:]' < version.txt)
export GO_VERSION

# Local build identity: denisitpro/lux-l7:local-<sha7>.
CI_COMMIT_SHORT_SHA := $(shell git rev-parse --short=7 HEAD)
CI_COMMIT_REF_NAME := $(shell git rev-parse --abbrev-ref HEAD)
CI_COMMIT_TIMESTAMP := $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
IMAGE_TAG := local-$(CI_COMMIT_SHORT_SHA)
BUILD_ID ?= $(IMAGE_TAG)
export CI_COMMIT_SHORT_SHA CI_COMMIT_REF_NAME CI_COMMIT_TIMESTAMP IMAGE_TAG BUILD_ID

.PHONY: help run test lint check-go-version docker docker-host vuln check

help:
	@echo "run              go run ./cmd/lux-l7"
	@echo "test             fast unit tests (go test -race ./...)"
	@echo "lint             golangci-lint"
	@echo "check-go-version assert go.mod matches version.txt"
	@echo "docker           build the image locally (denisitpro/lux-l7:local-<sha7>)"
	@echo "docker-host      build the -host variant (file capability) FROM the local image; run make docker first"
	@echo "vuln             govulncheck ./..."
	@echo "check            MANDATORY local smoke before reporting done: check-go-version lint test vuln"

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

# The Dockerfile has no ARG defaults: every value is passed explicitly.
docker: check-go-version
	docker build \
		--build-arg GO_VERSION=$(GO_VERSION) \
		--build-arg BIN=lux-l7 \
		--build-arg BUILD_ID=$(BUILD_ID) \
		--build-arg CI_COMMIT_SHORT_SHA=$(CI_COMMIT_SHORT_SHA) \
		--build-arg CI_COMMIT_REF_NAME=$(CI_COMMIT_REF_NAME) \
		--build-arg CI_COMMIT_TIMESTAMP=$(CI_COMMIT_TIMESTAMP) \
		-t denisitpro/lux-l7:$(IMAGE_TAG) .

# The -host variant is built FROM the image `make docker` produced (same /service bytes, only
# the file capability differs); it does not recompile Go.
docker-host:
	docker build \
		--build-arg BASE_IMAGE=denisitpro/lux-l7:$(IMAGE_TAG) \
		-f Dockerfile.host \
		-t denisitpro/lux-l7:$(IMAGE_TAG)-host .

GOVULNCHECK_VERSION := v1.8.0

vuln:
	go run golang.org/x/vuln/cmd/govulncheck@$(GOVULNCHECK_VERSION) ./...

# Mandatory local smoke: run it before declaring any work done. CI runs only on main.
check: check-go-version lint test vuln
