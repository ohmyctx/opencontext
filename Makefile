BINARY_CONTEXTD := bin/contextd
BINARY_OC       := bin/oc

VERSION  := $(shell git describe --tags --always --dirty 2>/dev/null || echo "0.1.0-dev")
LDFLAGS  := -ldflags "-X main.version=$(VERSION)"

.DEFAULT_GOAL := build

# ── Build ──────────────────────────────────────────────────────────────────────

.PHONY: build
build: $(BINARY_CONTEXTD) $(BINARY_OC)

$(BINARY_CONTEXTD): $(shell find cmd/contextd internal pkg -name '*.go')
	@mkdir -p bin
	go build $(LDFLAGS) -o $@ ./cmd/contextd

$(BINARY_OC): $(shell find cmd/oc internal pkg -name '*.go')
	@mkdir -p bin
	go build $(LDFLAGS) -o $@ ./cmd/oc

.PHONY: install
install:
	go install $(LDFLAGS) ./cmd/contextd ./cmd/oc

# ── Dev ────────────────────────────────────────────────────────────────────────

.PHONY: run
run: $(BINARY_CONTEXTD)
	./$(BINARY_CONTEXTD) --log-level debug

.PHONY: tidy
tidy:
	go mod tidy

# ── Test & Lint ────────────────────────────────────────────────────────────────

.PHONY: test
test:
	go test ./...

.PHONY: test-verbose
test-verbose:
	go test -v ./...

.PHONY: vet
vet:
	go vet ./...

.PHONY: lint
lint:
	@which golangci-lint >/dev/null 2>&1 || (echo "golangci-lint not found; install from https://golangci-lint.run/usage/install/" && exit 1)
	golangci-lint run ./...

.PHONY: check
check: vet test

# ── Clean ──────────────────────────────────────────────────────────────────────

.PHONY: clean
clean:
	rm -rf bin/

# ── Release ────────────────────────────────────────────────────────────────────

PLATFORMS := linux/amd64 linux/arm64 darwin/amd64 darwin/arm64

.PHONY: release
release:
	@mkdir -p dist
	@for platform in $(PLATFORMS); do \
		os=$$(echo $$platform | cut -d/ -f1); \
		arch=$$(echo $$platform | cut -d/ -f2); \
		echo "Building $$os/$$arch..."; \
		GOOS=$$os GOARCH=$$arch go build $(LDFLAGS) -o dist/contextd-$$os-$$arch ./cmd/contextd; \
		GOOS=$$os GOARCH=$$arch go build $(LDFLAGS) -o dist/oc-$$os-$$arch ./cmd/oc; \
	done
	@echo "Binaries written to dist/"

# ── Help ───────────────────────────────────────────────────────────────────────

.PHONY: help
help:
	@echo "Usage: make <target>"
	@echo ""
	@echo "  build         Build contextd and oc binaries to bin/"
	@echo "  install       Install binaries to GOPATH/bin"
	@echo "  run           Build and start contextd in debug mode"
	@echo "  tidy          Run go mod tidy"
	@echo "  test          Run all tests"
	@echo "  vet           Run go vet"
	@echo "  lint          Run golangci-lint (must be installed)"
	@echo "  check         Run vet + test"
	@echo "  clean         Remove bin/"
	@echo "  release       Cross-compile for linux/darwin amd64/arm64"
