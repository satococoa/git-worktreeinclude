SHELL := /bin/sh

GOFILES := $(shell git ls-files -- '*.go' | while IFS= read -r f; do [ -f "$$f" ] && printf '%s\n' "$$f"; done)
GO_TEST_CACHE_DIR := $(CURDIR)/.cache/go-build
GO_TEST_TMP_DIR := $(CURDIR)/.cache/go-tmp
GO_BIN_DIR := $(CURDIR)/.cache/bin
GO_RUN_ENV = GOCACHE="$(GO_TEST_CACHE_DIR)" GOTMPDIR="$(GO_TEST_TMP_DIR)"
GOLANGCI_LINT_VERSION := v2.10.1
GOLANGCI_LINT := $(GO_BIN_DIR)/golangci-lint

.PHONY: fmt check-fmt vet lint test test-race ci

fmt:
	@if [ -n "$(GOFILES)" ]; then \
		gofmt -s -w $(GOFILES); \
	fi

check-fmt:
	@if [ -n "$(GOFILES)" ]; then \
		out="$$(gofmt -s -l $(GOFILES))"; \
		if [ -n "$$out" ]; then \
			echo "The following files are not gofmt -s formatted:"; \
			echo "$$out"; \
			exit 1; \
		fi; \
	fi

vet:
	@mkdir -p "$(GO_TEST_CACHE_DIR)" "$(GO_TEST_TMP_DIR)"
	$(GO_RUN_ENV) go vet ./...

$(GOLANGCI_LINT):
	@mkdir -p "$(GO_TEST_CACHE_DIR)" "$(GO_TEST_TMP_DIR)" "$(GO_BIN_DIR)"
	GOBIN="$(GO_BIN_DIR)" $(GO_RUN_ENV) go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION)

lint: $(GOLANGCI_LINT)
	$(GO_RUN_ENV) $(GOLANGCI_LINT) run

test:
	@mkdir -p "$(GO_TEST_CACHE_DIR)" "$(GO_TEST_TMP_DIR)"
	$(GO_RUN_ENV) go test ./...

test-race:
	@mkdir -p "$(GO_TEST_CACHE_DIR)" "$(GO_TEST_TMP_DIR)"
	$(GO_RUN_ENV) go test -race ./...

ci: check-fmt vet lint test test-race
