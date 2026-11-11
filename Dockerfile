# Multi-stage build for Galactic Bloodshed
#
# LEGACY (2026-10-30): kept only because CI's `docker` job still builds it.
# The Phase 2 image specification is deploy/docker/Dockerfile (context `go/`,
# targets `server` and `gbctl`, distroless, UID 65532, digest-pinned bases;
# see deploy/docker/README.md and ADR-0006). This file runs as root on an
# unpinned alpine runtime and is not used by any deployment manifest; it is
# replaced by a pointer when the CI job is retargeted in the next stage.

# Stage 1: Build all Go binaries.
# Builder pinned to the same patched Go toolchain CI uses (1.27.4). The digest
# is the multi-architecture index digest reported by Docker Hub's registry
# (GET /v2/library/golang/manifests/1.27.4-alpine, Docker-Content-Digest,
# 2026-11-11). GOTOOLCHAIN=local forbids the Go command from switching
# toolchains; the RUN assertion fails the build if the image is not go1.27.4.
FROM golang:1.27.4-alpine@sha256:0a5f7fe796599ff487605987432e7619ae45beeb2fba4c2ce0b907879932aebf AS builder
ENV GOTOOLCHAIN=local
RUN go version && (go version | grep -q 'go1.27.4 ' || { echo "builder toolchain is not go1.27.4" >&2; exit 1; })
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
