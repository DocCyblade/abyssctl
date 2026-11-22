.DEFAULT_GOAL := build

# Pinned dev tools, installed into ./bin by `make tools`. They never enter go.mod.
GOLANGCI_LINT_VERSION := v2.15.0
GOTESTSUM_VERSION     := v1.13.0

BIN := $(CURDIR)/bin
BI  := github.com/DocCyblade/abyssctl/internal/buildinfo

# The toolchain go.mod selects, e.g. go1.27.4.
GO_TOOLCHAIN := $(shell go env GOVERSION)

VERSION       ?= 0.0.0-dev
COMMIT        ?= $(shell git rev-parse HEAD)
LDFLAGS_EXTRA ?=
ARCHES        := amd64 arm64

# In CI, make test also writes a JUnit report.
JUNIT_FILE ?= junit.xml
ifdef CI
GOTESTSUM_FLAGS := --junitfile $(JUNIT_FILE)
endif

.PHONY: tools lint test build

# `go install tool@version` ignores go.mod, so GOTOOLCHAIN pins the tools to the repo's toolchain: golangci-lint
# refuses a module whose Go version is newer than the one it was built with.
tools:
	GOBIN=$(BIN) GOTOOLCHAIN=$(GO_TOOLCHAIN) go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION)
	GOBIN=$(BIN) GOTOOLCHAIN=$(GO_TOOLCHAIN) go install gotest.tools/gotestsum@$(GOTESTSUM_VERSION)

lint:
	$(BIN)/golangci-lint run

test:
	CGO_ENABLED=1 $(BIN)/gotestsum $(GOTESTSUM_FLAGS) -- -race ./...

build:
	@mkdir -p dist
	$(foreach arch,$(ARCHES),CGO_ENABLED=0 GOOS=linux GOARCH=$(arch) go build -trimpath \
	  -ldflags "-s -w -X $(BI).Version=$(VERSION) -X $(BI).Commit=$(COMMIT) $(LDFLAGS_EXTRA)" \
	  -o dist/abyssctl-linux-$(arch) ./cmd/abyssctl &&) true
