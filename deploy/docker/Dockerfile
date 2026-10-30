# syntax=docker/dockerfile:1
#
# Phase 2 image build for the Galactic Bloodshed Go port (migration plan
# 11.2, ADR-0006). One multi-stage file, one --target per binary:
#
#   docker build -f deploy/docker/Dockerfile --target server -t gb-server:dev go
#   docker build -f deploy/docker/Dockerfile --target gbctl  -t gbctl:dev     go
#
# The build context is the Go module directory `go/` (the tracked module,
# nothing from source/ or docs/), filtered further by go/.dockerignore.
#
# Pinned bases (image INDEX digests reported by the registries themselves on
# 2026-10-30, Docker-Content-Digest of GET /v2/<repo>/manifests/<tag> with
# the OCI index media type; see deploy/docker/README.md for the exact
# commands and the per-platform manifest digests):
#   golang:1.27.3-bookworm                sha256:d9b6a1beb9474adc2a1ce842573639b44b2a0dd49b6669f51d3821baf9ec37c9
#   gcr.io/distroless/static-debian12:nonroot  sha256:afa5c872c891853ca7fcf1f12c3edb23f7eeef36189728842dd51042ff57f7ab
#
# NOT BUILT OR RUN ANYWHERE YET: no container runtime exists on the
# development host by policy; the first real build happens in the GitHub
# Actions job added in the next stage. Everything below is static.

# ---------------------------------------------------------------- builder --
FROM golang:1.27.3-bookworm@sha256:d9b6a1beb9474adc2a1ce842573639b44b2a0dd49b6669f51d3821baf9ec37c9 AS builder

# The reviewed, patched toolchain CI uses (go.mod: go 1.25.0, toolchain
# go1.27.3). GOTOOLCHAIN=local forbids silent toolchain switching; the RUN
# assertion fails the build if the base image is not go1.27.3.
ENV GOTOOLCHAIN=local \
    GOFLAGS=-mod=readonly \
    CGO_ENABLED=0 \
    GOOS=linux \
    GOARCH=amd64
RUN go version && (go version | grep -q 'go1.27.3 ' || { echo "builder toolchain is not go1.27.3" >&2; exit 1; })

WORKDIR /src

# Dependencies first (cache-friendly), verified against go.sum before any
# source is compiled.
COPY go.mod go.sum ./
RUN go mod download && go mod verify

COPY . .

# Reproducible settings: no VCS stamping (the digest is the identity),
# -trimpath removes the build directory, -buildid= removes the per-build id,
# -s -w strips symbol and DWARF tables. CGO is off, so the binaries are
# static and run on distroless/static.
RUN go build -trimpath -buildvcs=false -ldflags='-s -w -buildid=' -o /out/gb-server ./cmd/gb-server \
 && go build -trimpath -buildvcs=false -ldflags='-s -w -buildid=' -o /out/gbctl     ./cmd/gbctl \
 && ls -l /out

# ----------------------------------------------------------------- server --
# distroless/static: CA certificates, tzdata, /etc/passwd with user nonroot
# (UID/GID 65532), no shell, no package manager. The image's own default
# USER is 65532; it is repeated here so a future base change cannot
# silently reintroduce root.
FROM gcr.io/distroless/static-debian12:nonroot@sha256:afa5c872c891853ca7fcf1f12c3edb23f7eeef36189728842dd51042ff57f7ab AS server

COPY --from=builder --chown=65532:65532 /out/gb-server /usr/local/bin/gb-server

# The code default (localhost:8080) is unreachable from outside a container;
# the image binds the player listener on all interfaces. The admin listener
# keeps the code default (127.0.0.1:8081) unless the deployment sets
# GB_ADMIN_ADDR (the Kubernetes manifests set 0.0.0.0:8081 under a
# NetworkPolicy). Secrets come from GB_JWT_SECRET_FILE / GB_ADMIN_TOKEN_FILE
# (mounted Secret) and the database path from GB_DB_PATH on a mounted
# volume: the root filesystem is read-only in the manifests and this image
# writes nothing outside the volume. See docs/operations/local-server.md
# for every variable.
ENV GB_ADDR=0.0.0.0:8080

USER 65532:65532
EXPOSE 8080 8081
ENTRYPOINT ["/usr/local/bin/gb-server"]

# ------------------------------------------------------------------ gbctl --
# Operator CLI, for the CI kind job and for one-off `kubectl run` use. It
# takes the token only from GBCTL_TOKEN / GBCTL_TOKEN_FILE, never from an
# argument, and talks to GBCTL_SERVER (default http://127.0.0.1:8081).
FROM gcr.io/distroless/static-debian12:nonroot@sha256:afa5c872c891853ca7fcf1f12c3edb23f7eeef36189728842dd51042ff57f7ab AS gbctl

COPY --from=builder --chown=65532:65532 /out/gbctl /usr/local/bin/gbctl

USER 65532:65532
ENTRYPOINT ["/usr/local/bin/gbctl"]
