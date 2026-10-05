.DEFAULT_GOAL := help

GO_VERSION := $(shell tr -d '[:space:]' < version.txt)
export GO_VERSION

.PHONY: help run test lint check-go-version

help:
	@echo "run              go run ./cmd/lux-l7"
	@echo "test             go test ./..."
	@echo "lint             golangci-lint"
	@echo "check-go-version assert go.mod matches version.txt"
	@echo "docker-build     docker build (GO_VERSION from version.txt)"

run:
	go run ./cmd/lux-l7

test:
	go test ./...

lint:
	PATH="$$(go env GOPATH)/bin:$$PATH" golangci-lint run ./...

check-go-version:
	@test "$$(go env GOVERSION)" = "go$(GO_VERSION)"

docker-build: check-go-version
	docker build \
		--build-arg GO_VERSION=$(GO_VERSION) \
		--build-arg BIN=lux-l7 \
		-t denisitpro/lux-l7:local .
