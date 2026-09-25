BINARY      := bitcoin-exporter
PKG         := ./cmd/bitcoin-exporter
VERSION     ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
NODE_IMAGE  ?= ghcr.io/thefutoneng/bitcoin:31.1-1
IMAGE       ?= bitcoin-prometheus-exporter
LDFLAGS     := -s -w -X main.version=$(VERSION)

# Where a running stack is reachable. Override both when
# docker-compose.override.yml republishes the stack off loopback, e.g.
#   make metrics EXPORTER_URL=http://192.168.1.6:19332
EXPORTER_URL   ?= http://127.0.0.1:9332
PROMETHEUS_URL ?= http://127.0.0.1:9090
export PROMETHEUS_URL

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
	docker compose down -v --remove-orphans

.PHONY: logs
logs: ## Follow the exporter's logs
	docker compose logs -f exporter

.PHONY: metrics
metrics: ## Print the exporter's current metrics
	@curl -fsS $(EXPORTER_URL)/metrics

.PHONY: check-stack
check-stack: ## Run the alert-rule and dashboard checks CI runs against a live stack
	python3 scripts/check-alert-rules.py
	python3 scripts/check-dashboard-queries.py

.PHONY: activity
activity: ## Mine a block and broadcast transactions on the regtest stack
	./scripts/regtest-activity.sh

.PHONY: clean
clean: ## Remove build output
	rm -rf dist
