# Aracne — build, test, lint.
#
# CGO is required and not optional: the JavaScript, TypeScript, Rust and Java scanners are
# tree-sitter, so you need a C compiler (gcc). SQLite is pure-Go, so no C SQLite is needed.

BINARY      := arac
PKG         := ./cmd/arac
BIN_DIR     := bin
VERSION     := $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS     := -s -w -X main.Version=$(VERSION)
GO          ?= go

export CGO_ENABLED = 1

.DEFAULT_GOAL := build
.PHONY: build build-basic all test test-minimal lint fmt vet tidy clean install version help

## build: the Full build — engine, every language, and the web visualizer (default)
build:
	$(GO) build -ldflags "$(LDFLAGS)" -o $(BIN_DIR)/$(BINARY) $(PKG)

## build-basic: the Basic build — same engine, no front-end, no embedded SPA
build-basic:
	$(GO) build -tags minimal -ldflags "$(LDFLAGS)" -o $(BIN_DIR)/$(BINARY)-basic $(PKG)

## all: both builds, side by side
all: build build-basic
	@ls -lh $(BIN_DIR)/$(BINARY) $(BIN_DIR)/$(BINARY)-basic

## test: the whole suite (the at-scale corpus scans make this take ~1 minute)
test:
	$(GO) test ./...

## test-minimal: the suite under -tags minimal, so the Basic build cannot rot
test-minimal:
	$(GO) test -tags minimal ./...

## lint: golangci-lint, if installed
lint:
	@command -v golangci-lint >/dev/null 2>&1 \
		|| { echo "golangci-lint not installed: https://golangci-lint.run/welcome/install/"; exit 1; }
	golangci-lint run

## vet: go vet over both tag sets
vet:
	$(GO) vet ./...
	$(GO) vet -tags minimal ./...

## fmt: gofmt the source. testing_ground/ is EXCLUDED on purpose — it is a deliberately
## malformed corpus, and reformatting it shifts line numbers the at-scale suites assert on.
fmt:
	gofmt -w ./cmd ./internal ./tests

## tidy: go mod tidy
tidy:
	$(GO) mod tidy

## install: install the Full build into GOBIN
install:
	$(GO) install -ldflags "$(LDFLAGS)" $(PKG)

## version: the version string this build would stamp
version:
	@echo $(VERSION)

## clean: remove build output
clean:
	rm -rf $(BIN_DIR)

## help: list targets
help:
	@grep -E '^## ' $(MAKEFILE_LIST) | sed 's/^## /  /'
