SHELL := /usr/bin/env bash
.SHELLFLAGS := -eu -o pipefail -c

REGISTRY ?= harbor.szymonrichert.pl
IMAGE_NAME ?= tatara-memory
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
DATE ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)

IMAGE_REF := $(REGISTRY)/$(IMAGE_NAME):$(VERSION)

.PHONY: all lint test build image push chart-lint helmfile-lint chart-test chart-push tidy fmt clean

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
	docker build \
		--build-arg VERSION=$(VERSION) \
		--build-arg COMMIT=$(COMMIT) \
		--build-arg DATE=$(DATE) \
		-t $(IMAGE_REF) \
		.

push: image
	docker push $(IMAGE_REF)

chart-lint:
	mise exec -- helm lint charts/tatara-memory

helmfile-lint:
	mise exec -- helmfile lint

chart-test:
	helm unittest charts/tatara-memory

chart-push:
	helm package charts/tatara-memory -d dist/
	helm push dist/tatara-memory-*.tgz oci://$(REGISTRY)/charts

clean:
	rm -rf bin dist
