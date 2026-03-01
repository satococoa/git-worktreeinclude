SHELL := /bin/sh

GOFILES := $(shell git ls-files '*.go')

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
	go vet ./...

lint:
	@if ! command -v golangci-lint >/dev/null 2>&1; then \
		echo "golangci-lint is not installed."; \
		echo "Install with:"; \
		echo "  go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.10.1"; \
		exit 1; \
	fi
	golangci-lint run ./...

test:
	go test ./...

test-race:
	go test -race ./...

ci: check-fmt vet lint test test-race
