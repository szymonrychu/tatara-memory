SHELL := /usr/bin/env bash
.SHELLFLAGS := -eu -o pipefail -c

REGISTRY ?= harbor.szymonrichert.pl
IMAGE_NAME ?= containers/tatara-memory
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
DATE ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)

IMAGE_REF := $(REGISTRY)/$(IMAGE_NAME):$(VERSION)

# Resolve helm binary via mise to avoid homebrew helm 4.x shadow.
# mise exec sets a PATH that includes the versioned helm dir before homebrew.
HELM_BIN := $(shell mise exec -- bash -c 'echo $$PATH' | tr ':' '\n' | grep -m1 'mise/installs/helm')
ifdef HELM_BIN
HELM_BIN := $(HELM_BIN)/helm
else
HELM_BIN := helm
endif

# helm-unittest plugin requires helm 4.x plugin API (platformHooks). The mise-managed
# helm 3.16 cannot load the plugin. Use brew-installed helm 4.x (direct path) for
# chart-test so the unittest plugin resolves correctly.
HELM_UNITTEST_BIN := $(shell ls /opt/homebrew/bin/helm 2>/dev/null || echo $(HELM_BIN))

.PHONY: all lint test build image chart-lint chart-test tidy fmt clean ci

all: lint test build

tidy:
	go mod tidy

fmt:
	gofmt -s -w .
	goimports -w -local github.com/szymonrychu/tatara-memory .

lint:
	golangci-lint run ./... || [ $$? -eq 5 ]

test:
	go test ./... -race -count=1

build:
	CGO_ENABLED=0 go build \
		-trimpath \
		-ldflags "-s -w \
		  -X github.com/szymonrychu/tatara-memory/internal/version.Version=$(VERSION) \
		  -X github.com/szymonrychu/tatara-memory/internal/version.Commit=$(COMMIT) \
		  -X github.com/szymonrychu/tatara-memory/internal/version.Date=$(DATE)" \
		-o bin/tatara-memory \
		./cmd/tatara-memory

image:
	docker buildx build \
		--platform=linux/amd64 \
		--build-arg VERSION=$(VERSION) \
		--build-arg COMMIT=$(COMMIT) \
		--build-arg DATE=$(DATE) \
		-t $(IMAGE_REF) \
		--load \
		.

chart-lint:
	$(HELM_BIN) lint charts/tatara-memory

chart-test:
	$(HELM_UNITTEST_BIN) unittest charts/tatara-memory

ci: lint test

clean:
	rm -rf bin dist
