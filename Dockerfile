# Multi-stage build for Galactic Bloodshed
#
# LEGACY (2026-10-30): kept only because CI's `docker` job still builds it.
# The Phase 2 image specification is deploy/docker/Dockerfile (context `go/`,
# targets `server` and `gbctl`, distroless, UID 65532, digest-pinned bases;
# see deploy/docker/README.md and ADR-0006). This file runs as root on an
# unpinned alpine runtime and is not used by any deployment manifest; it is
# replaced by a pointer when the CI job is retargeted in the next stage.

# Stage 1: Build all Go binaries.
# Builder pinned to the same patched Go toolchain CI uses (1.27.3). The digest
# is the multi-architecture index digest reported by Docker Hub's registry
# (HEAD /v2/library/golang/manifests/1.27.3-alpine, Docker-Content-Digest,
# 2026-10-28). GOTOOLCHAIN=local forbids the Go command from switching
# toolchains; the RUN assertion fails the build if the image is not go1.27.3.
FROM golang:1.27.3-alpine@sha256:d200d2dd76b03a99b52651fd1325aff7dcd35422c35c01bac56571b0507f3deb AS builder
ENV GOTOOLCHAIN=local
RUN go version && (go version | grep -q 'go1.27.3 ' || { echo "builder toolchain is not go1.27.3" >&2; exit 1; })
WORKDIR /src
COPY . .

RUN cd go && \
    go build -o /build/makeuniv ./cmd/makeuniv && \
    go build -o /build/gb-server ./cmd/gb-server && \
    go build -o /build/racegen ./cmd/racegen

# Stage 2: Runtime image
FROM alpine:latest

# Install runtime dependencies
RUN apk add --no-cache ca-certificates

# Copy binaries from builder
COPY --from=builder /build/makeuniv /usr/local/bin/makeuniv
COPY --from=builder /build/gb-server /usr/local/bin/gb-server
COPY --from=builder /build/racegen /usr/local/bin/racegen

# Create data directory
RUN mkdir -p /data

# Default command is gb-server
ENTRYPOINT []
CMD ["gb-server"]
