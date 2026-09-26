# syntax=docker/dockerfile:1

# Both bases are pinned by digest, not tag. A tag is a moving target, and the
# release claims the published image is bit-for-bit what this tree builds — a
# base that shifted underneath it would break that silently. Keep these in sync
# with GO_BASE and RUNTIME_BASE in the Makefile; check-pins.sh asserts it.
ARG GO_BASE=golang:1.26.2-bookworm@sha256:47ce5636e9936b2c5cbf708925578ef386b4f8872aec74a67bd13a627d242b19
ARG RUNTIME_BASE=gcr.io/distroless/static-debian12:nonroot@sha256:afa5c872c891853ca7fcf1f12c3edb23f7eeef36189728842dd51042ff57f7ab

FROM ${GO_BASE} AS build
WORKDIR /src

# Dependencies first so edits to the source do not re-download modules.
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download

COPY . .

# VERSION and REVISION are stamped into the binary and reported by
# bitcoin_exporter_build_info. They are build inputs, so two builds of the same
# tag agree; a build of a different commit is expected to differ.
ARG VERSION=dev
ARG REVISION=unknown
ARG TARGETOS
ARG TARGETARCH

# What makes this reproducible:
#   -trimpath        strips the build path, which differs between machines.
#   CGO_ENABLED=0    no linking against a host libc.
#   -buildvcs=false  .dockerignore excludes .git so the stamp would be empty
#                    anyway; saying so explicitly keeps it from depending on
#                    whether the build context happened to include it.
#   -s -w            drops the symbol and DWARF tables, which is also why two
#                    builds cannot differ in debug metadata.
# Timestamps come from SOURCE_DATE_EPOCH, which buildkit applies to the layers.
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build -trimpath -buildvcs=false \
      -ldflags "-s -w -X main.version=${VERSION} -X main.revision=${REVISION}" \
      -o /out/bitcoin-exporter ./cmd/bitcoin-exporter

FROM ${RUNTIME_BASE}
COPY --from=build /out/bitcoin-exporter /usr/local/bin/bitcoin-exporter

# Matches the uid the node image runs as, so a shared data volume's cookie
# file stays readable.
USER 65532:65532
EXPOSE 9332
ENTRYPOINT ["/usr/local/bin/bitcoin-exporter"]

ARG VERSION
ARG REVISION
LABEL org.opencontainers.image.title="bitcoin-prometheus-exporter" \
      org.opencontainers.image.description="Prometheus exporter for a Bitcoin Core node" \
      org.opencontainers.image.source="https://github.com/TheFutonEng/bitcoin-prometheus-exporter" \
      org.opencontainers.image.licenses="Apache-2.0" \
      org.opencontainers.image.version="${VERSION}" \
      org.opencontainers.image.revision="${REVISION}"
