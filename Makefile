BINARY   := fathom
PKG      := ./cmd/fathom
VERSION  ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT   := $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
BUILD_TS := $(shell date -u '+%Y%m%d.%H%M')
VPKG     := github.com/tonhe/fathom/internal/version
LDFLAGS  := -trimpath -ldflags "-s -w -X $(VPKG).Version=$(VERSION) -X $(VPKG).Commit=$(COMMIT) -X $(VPKG).Build=$(BUILD_TS)"

.PHONY: build clean install test test-race test-all test-tui golden-update coverage-gate test-integration release-dry-run demo

build:
	go build $(LDFLAGS) -o bin/$(BINARY) $(PKG)

clean:
	rm -rf bin/ dist/

# Unlink before copy so the destination gets a fresh inode (avoids the macOS
# AMFI cached-cdhash kill on ad-hoc-signed binaries).
install: build
	mkdir -p $(HOME)/bin
	rm -f $(HOME)/bin/$(BINARY)
	cp bin/$(BINARY) $(HOME)/bin/$(BINARY)

test:
	go test ./...

test-race:
	go test -race ./...

test-all: build test-race coverage-gate
	@echo
	@echo "All checks passed. Safe to push."

test-tui:
	go test -race ./internal/app/... ./internal/ui/... ./internal/modules/...

golden-update:
	go test ./internal/... -update-goldens

coverage-gate:
	bash scripts/check-coverage.sh

test-integration:
	go test -tags integration -timeout 20m ./test/integration/...

release-dry-run:
	goreleaser release --snapshot --clean

demo: build
	./bin/$(BINARY) --demo
