BINARY      := bitcoin-exporter
PKG         := ./cmd/bitcoin-exporter

# The release version, without the leading v. Derived from the tag so a local
# `make image` names itself sensibly; the release passes it explicitly and the
# guard job checks the tag agrees.
VERSION     ?= $(patsubst v%,%,$(shell git describe --tags --abbrev=0 2>/dev/null || echo 0.0.0))
REVISION    ?= $(shell git rev-parse HEAD 2>/dev/null || echo unknown)

# Layer timestamps. Taken from the commit so it is a property of the tree
# rather than of when you happened to build — which is the whole reason two
# builds of a tag agree. Verified: changing it changes the image digest, so
# this is load-bearing, not decoration.
SOURCE_DATE_EPOCH ?= $(shell git log -1 --format=%ct 2>/dev/null || echo 0)

REGISTRY    ?= ghcr.io/thefutoneng
IMAGE       ?= $(REGISTRY)/bitcoin-prometheus-exporter
TAG         ?= $(VERSION)
IMAGE_REF   := $(IMAGE):$(TAG)

# v0.1.0 publishes 0.1.0, 0.1 and latest. $(basename) drops the last
# dot-suffix, so 0.1.0 -> 0.1. A prerelease (0.2.0-rc1) must not move `latest`
# or the floating minor tag, so it publishes only its exact version.
MAJOR_MINOR := $(basename $(VERSION))
ifeq (,$(findstring -,$(VERSION)))
PUBLISH_TAGS ?= $(VERSION) $(MAJOR_MINOR) latest
else
PUBLISH_TAGS ?= $(VERSION)
endif
PUBLISH_NAMES := $(foreach t,$(PUBLISH_TAGS),$(IMAGE):$(t))

# The platforms a release publishes, together, as one OCI index. Also the set
# the reproducibility claim covers.
PLATFORMS   ?= linux/amd64 linux/arm64
# A single platform, for the targets that act on one at a time.
PLATFORM    ?= linux/amd64

# Pinned by digest, and kept identical to the ARGs in the Dockerfile —
# check-pins asserts it. A moving tag would quietly break the claim that the
# published image is what this tree builds.
GO_BASE      ?= golang:1.26.2-bookworm@sha256:47ce5636e9936b2c5cbf708925578ef386b4f8872aec74a67bd13a627d242b19
RUNTIME_BASE ?= gcr.io/distroless/static-debian12:nonroot@sha256:afa5c872c891853ca7fcf1f12c3edb23f7eeef36189728842dd51042ff57f7ab

# The push and any rebuild compared against it must assemble layers with the
# SAME buildkit, or a digest comparison is between two different builders.
BUILDKIT_IMAGE ?= moby/buildkit:v0.32.2
# The builder used for release-shaped builds. The default `docker` driver
# cannot produce attestations at all.
BUILDER     ?= release

LDFLAGS     := -s -w -X main.version=$(VERSION) -X main.revision=$(REVISION)

# Where a running stack is reachable. Override both when
# docker-compose.override.yml republishes the stack off loopback, e.g.
#   make metrics EXPORTER_URL=http://192.168.1.6:19332
EXPORTER_URL   ?= http://127.0.0.1:9332
PROMETHEUS_URL ?= http://127.0.0.1:9090
export PROMETHEUS_URL

NODE_IMAGE  ?= ghcr.io/thefutoneng/bitcoin:31.1-1

export SOURCE_DATE_EPOCH

comma := ,
space := $(subst ,, )

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

.PHONY: print-version print-revision print-platforms print-image-ref print-buildkit-image print-source-date-epoch print-go-base print-runtime-base print-builder print-publish-tags
print-version:            ; @echo $(VERSION)
print-revision:           ; @echo $(REVISION)
print-platforms:          ; @echo $(PLATFORMS)
print-image-ref:          ; @echo $(IMAGE_REF)
print-buildkit-image:     ; @echo $(BUILDKIT_IMAGE)
print-source-date-epoch:  ; @echo $(SOURCE_DATE_EPOCH)
print-go-base:            ; @echo $(GO_BASE)
print-builder:            ; @echo $(BUILDER)
print-publish-tags:       ; @echo $(PUBLISH_TAGS)
print-runtime-base:       ; @echo $(RUNTIME_BASE)

.PHONY: check-pins
check-pins: ## Assert the Dockerfile and Makefile pin the same bases
	@scripts/check-pins.sh

.PHONY: builder
builder: ## Create the buildx builder releases use (idempotent)
	@docker buildx inspect $(BUILDER) >/dev/null 2>&1 \
	  || docker buildx create --name $(BUILDER) --driver docker-container \
	       --driver-opt "image=$(BUILDKIT_IMAGE)"
	@docker buildx use $(BUILDER) >/dev/null

.PHONY: image
image: check-pins builder ## Build the image for one platform into the local docker
	docker buildx --builder $(BUILDER) build --platform $(PLATFORM) --load \
	  --build-arg VERSION=$(VERSION) --build-arg REVISION=$(REVISION) \
	  --build-arg SOURCE_DATE_EPOCH=$(SOURCE_DATE_EPOCH) \
	  --provenance=false --sbom=false \
	  -t $(IMAGE_REF) .

.PHONY: smoke
smoke: ## Boot the local image and prove it serves metrics
	@scripts/smoke-image.sh $(IMAGE_REF)

.PHONY: push
# Tags go in as repeated -t flags, not as a comma-separated name= inside
# --output: the output spec is itself CSV, so a comma in a value is read as the
# next field and buildx rejects it ("invalid value ...:0.1").
push: check-pins builder ## Build every platform and push one index, with attestations
	docker buildx --builder $(BUILDER) build --platform $(subst $(space),$(comma),$(PLATFORMS)) \
	  $(foreach n,$(PUBLISH_NAMES),-t $(n)) \
	  --build-arg VERSION=$(VERSION) --build-arg REVISION=$(REVISION) \
	  --build-arg SOURCE_DATE_EPOCH=$(SOURCE_DATE_EPOCH) \
	  --provenance=mode=max --sbom=true \
	  --output "type=image,push=true,rewrite-timestamp=true" .

.PHONY: digest-ref
digest-ref: ## Print the published index as image@sha256:... — this is what consumers pin
	@printf '%s@%s\n' "$(IMAGE)" \
	  "$$(docker buildx imagetools inspect $(IMAGE_REF) --format '{{ .Manifest.Digest }}')"

.PHONY: platform-ref
platform-ref: ## Print one platform's published image manifest digest
	@scripts/platform-ref.sh "$(IMAGE_REF)" "$(PLATFORM)"

.PHONY: repro-digest
repro-digest: check-pins builder ## Rebuild PLATFORM locally and print its image manifest digest
	@scripts/repro-digest.sh "$(PLATFORM)"

.PHONY: verify-repro-published
verify-repro-published: ## Prove the published PLATFORM image is bit-for-bit this tree
	@scripts/verify-reproducible.sh "$(IMAGE_REF)" "$(PLATFORM)"

.PHONY: verify-published
verify-published: ## Pull the published PLATFORM image by digest and smoke it
	@scripts/verify-published.sh "$(IMAGE_REF)" "$(PLATFORM)"

.PHONY: sign
sign: ## Sign the index and every platform image (keyless and/or key pair)
	@scripts/sign-image.sh "$(IMAGE_REF)"

.PHONY: verify-sig
verify-sig: ## Verify whichever signing modes are configured
	@scripts/verify-signatures.sh "$(IMAGE_REF)"

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
