# syntax=docker/dockerfile:1

FROM golang:1.26-bookworm AS build
WORKDIR /src

# Dependencies first so edits to the source do not re-download modules.
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download

COPY . .
ARG VERSION=dev
ARG TARGETOS=linux
ARG TARGETARCH=amd64
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" \
      -o /out/bitcoin-exporter ./cmd/bitcoin-exporter

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/bitcoin-exporter /usr/local/bin/bitcoin-exporter

# Matches the uid the node image runs as, so a shared data volume's cookie
# file stays readable.
USER 65532:65532
EXPOSE 9332
ENTRYPOINT ["/usr/local/bin/bitcoin-exporter"]
