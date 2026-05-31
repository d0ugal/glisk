.PHONY: help build test lint clean fmt lint-only frontend dev-tag

# Docker image versions
GOLANGCI_LINT_VERSION := v2.12.2

help:
	@echo "Available targets:"
	@echo "  build     - Build the glisk binary"
	@echo "  test      - Run tests with coverage"
	@echo "  frontend  - Install deps and build the SPA"
	@echo "  lint      - Format code and run golangci-lint"
	@echo "  fmt       - Format code using golangci-lint"
	@echo "  lint-only - Run golangci-lint without formatting"
	@echo "  clean     - Clean build artifacts"
	@echo "  dev-tag   - Generate dev tag for Docker image"

# Build the binary. The frontend dist is embedded; run `make frontend` first
# for a production build (the committed .gitkeep keeps `go build` working).
build:
	go build -v -ldflags="-s -w" -o glisk .

test:
	go test -v -race -coverprofile=coverage.txt -covermode=atomic ./...

frontend:
	cd internal/webui/frontend && npm install && npm run build

fmt:
	docker run --rm \
		-u "$(shell id -u):$(shell id -g)" \
		-e GOCACHE=/tmp/go-cache \
		-e GOMODCACHE=/tmp/go-mod-cache \
		-e GOLANGCI_LINT_CACHE=/tmp/golangci-lint-cache \
		-v "$(PWD):/app" \
		-v "$(HOME)/.cache:/tmp/cache" \
		-w /app \
		golangci/golangci-lint:$(GOLANGCI_LINT_VERSION) \
		golangci-lint run --fix

lint:
	docker run --rm \
		-u "$(shell id -u):$(shell id -g)" \
		-e GOCACHE=/tmp/go-cache \
		-e GOMODCACHE=/tmp/go-mod-cache \
		-e GOLANGCI_LINT_CACHE=/tmp/golangci-lint-cache \
		-v "$(PWD):/app" \
		-v "$(HOME)/.cache:/tmp/cache" \
		-w /app \
		golangci/golangci-lint:$(GOLANGCI_LINT_VERSION) \
		golangci-lint run --fix

lint-only:
	docker run --rm \
		-u "$(shell id -u):$(shell id -g)" \
		-e GOCACHE=/tmp/go-cache \
		-e GOMODCACHE=/tmp/go-mod-cache \
		-e GOLANGCI_LINT_CACHE=/tmp/golangci-lint-cache \
		-v "$(PWD):/app" \
		-v "$(HOME)/.cache:/tmp/cache" \
		-w /app \
		golangci/golangci-lint:$(GOLANGCI_LINT_VERSION) \
		golangci-lint run

clean:
	rm -f glisk coverage.txt

dev-tag:
	@SHORT_SHA=$$(git rev-parse --short HEAD 2>/dev/null || echo "unknown"); \
	LAST_TAG=$$(git describe --tags --abbrev=0 --match="v[0-9]*.[0-9]*.[0-9]*" 2>/dev/null || echo ""); \
	if [ -z "$$LAST_TAG" ]; then \
		VERSION="0.0.0"; \
		COMMIT_COUNT=$$(git rev-list --count HEAD); \
	else \
		VERSION=$${LAST_TAG#v}; \
		COMMIT_COUNT=$$(git rev-list --count $${LAST_TAG}..HEAD); \
	fi; \
	DEV_TAG="v$${VERSION}-dev.$${COMMIT_COUNT}.$${SHORT_SHA}"; \
	echo "$$DEV_TAG"
