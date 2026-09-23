BINARY      := bitcoin-exporter
PKG         := ./cmd/bitcoin-exporter
VERSION     ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
NODE_IMAGE  ?= ghcr.io/thefutoneng/bitcoin:31.1
IMAGE       ?= bitcoin-prometheus-exporter
LDFLAGS     := -s -w -X main.version=$(VERSION)

.DEFAULT_GOAL := help

.PHONY: help
help: ## Show this help
	@grep -hE '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) | \
	  awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-18s\033[0m %s\n", $$1, $$2}'

.PHONY: build
build: ## Build the exporter binary into dist/
	@mkdir -p dist
	CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o dist/$(BINARY) $(PKG)

.PHONY: test
test: ## Run unit tests
	go test -race ./...

.PHONY: test-integration
test-integration: ## Run integration tests against a real node container
	NODE_IMAGE=$(NODE_IMAGE) go test -tags integration -timeout 10m ./internal/integration/

.PHONY: lint
lint: ## Run gofmt and go vet
	@unformatted=$$(gofmt -l . | grep -v '^dist/' || true); \
	 if [ -n "$$unformatted" ]; then echo "gofmt needed:"; echo "$$unformatted"; exit 1; fi
	go vet ./...
	go vet -tags integration ./...

.PHONY: image
image: ## Build the container image
	docker build --build-arg VERSION=$(VERSION) -t $(IMAGE):$(VERSION) -t $(IMAGE):dev .

.PHONY: up
up: ## Start the node, exporter, Prometheus and Grafana
	docker compose up -d --build

.PHONY: down
down: ## Stop the stack and delete its volumes
	docker compose down -v

.PHONY: logs
logs: ## Follow the exporter's logs
	docker compose logs -f exporter

.PHONY: metrics
metrics: ## Print the exporter's current metrics
	@curl -fsS http://127.0.0.1:9332/metrics

.PHONY: activity
activity: ## Mine a block and broadcast transactions on the regtest stack
	./scripts/regtest-activity.sh

.PHONY: clean
clean: ## Remove build output
	rm -rf dist
