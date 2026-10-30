# Container images (Phase 2)

**Status: specification only. No image has been built, run or published. Nothing here is approved for deployment.** The first real build is the GitHub Actions job of the next Phase 2 stage; the development host runs no container runtime by policy, so the Dockerfile has only been checked statically.

## Targets

| Target | Binary | Base | User | Entrypoint | Intended name |
|---|---|---|---|---|---|
| `server` | `go/cmd/gb-server` | `gcr.io/distroless/static-debian12:nonroot` | 65532:65532 | `/usr/local/bin/gb-server` | `ghcr.io/phreddrick/gb-server` |
| `gbctl` | `go/cmd/gbctl` | same | 65532:65532 | `/usr/local/bin/gbctl` | `ghcr.io/phreddrick/gbctl` |

Build context is `go/` (the tracked module), filtered by `go/.dockerignore`:

```sh
docker build -f deploy/docker/Dockerfile --target server -t gb-server:dev go
docker build -f deploy/docker/Dockerfile --target gbctl  -t gbctl:dev     go
```

The root `Dockerfile` (alpine runtime, root user, `makeuniv`/`racegen`/`gb-server`) is unchanged and still what CI's `docker` job builds; it is superseded by this file for deployment and will be reduced to a pointer when the CI job is retargeted.

## Pinned base images

Digests are the **image index** digests (multi-platform manifest lists) as reported by the registries on 2026-10-30, so they match what `docker pull <ref>@<digest>` resolves on any platform. They were read, not invented:

```sh
# Docker Hub (anonymous pull token)
TOKEN=$(curl -fsS 'https://auth.docker.io/token?service=registry.docker.io&scope=repository:library/golang:pull' | jq -r .token)
curl -fsSI -H "Authorization: Bearer $TOKEN" \
  -H 'Accept: application/vnd.oci.image.index.v1+json' \
  https://registry-1.docker.io/v2/library/golang/manifests/1.27.3-bookworm | grep -i docker-content-digest
# gcr.io (anonymous)
curl -fsSI -H 'Accept: application/vnd.oci.image.index.v1+json' \
  https://gcr.io/v2/distroless/static-debian12/manifests/nonroot | grep -i docker-content-digest
```

| Image | Index digest | linux/amd64 manifest digest (informational) |
|---|---|---|
| `golang:1.27.3-bookworm` | `sha256:d9b6a1beb9474adc2a1ce842573639b44b2a0dd49b6669f51d3821baf9ec37c9` | `sha256:e482b31b5d30c31f9e495f4b8cbdf41429b2d74955dfbcd9c0a904483d738390` |
| `gcr.io/distroless/static-debian12:nonroot` | `sha256:afa5c872c891853ca7fcf1f12c3edb23f7eeef36189728842dd51042ff57f7ab` | `sha256:52dcfbabb7457ea47c82f6e13af8c8a4a1d9f7b0145142b3ecab20f2b888411d` |

The distroless amd64 image config (fetched the same way) declares `User: 65532`, `WorkingDir: /home/nonroot`, `SSL_CERT_FILE=/etc/ssl/certs/ca-certificates.crt` and no entrypoint. Distroless `nonroot` tags move; the digest does not. Bumps are a reviewed commit that updates both the digest and this table.

## Runtime compatibility review (static)

- **Dependencies:** the server is pure Go with `CGO_ENABLED=0` (bbolt, gorilla/websocket, golang-jwt, stdlib). It needs no libc, no shell, no CA bundle (it makes no outbound TLS calls) and no timezone database (slog timestamps are UTC). distroless/static provides the first two anyway.
- **Writes:** the only file the server writes is the bbolt database at `GB_DB_PATH` (created mode 0600 by the process; the parent directory must already exist, `db.Open` does not create it). It writes nothing to `/tmp`, the working directory or `$HOME`, so `readOnlyRootFilesystem: true` is compatible. Logs go to stdout.
- **Reads:** `GB_DOCS_DIR` (default `./Docs`, relative to the image working directory `/home/nonroot`) holds optional player help files; when absent the `help` command answers "Help file not found." and nothing else is affected. No help files are shipped in the image.
- **Non-root and the volume:** the process runs as 65532; the Kubernetes manifests set `fsGroup: 65532` so the Longhorn ext4 mount is group-writable at mount time. No init container, no `chown`, no privileged step.
- **Secrets:** `GB_JWT_SECRET_FILE` and `GB_ADMIN_TOKEN_FILE` read mounted files; the plain variables must then be unset (the loader refuses both forms of one secret). The image sets neither.
- **Ports:** `EXPOSE 8080 8081` is documentation; the manifests decide what is reachable. `GB_ADDR=0.0.0.0:8080` is set in the image because the code default (`localhost`) is unreachable from outside a container; `GB_ADMIN_ADDR` is left to the deployment.
- **Signals:** the binary is PID 1 and handles SIGTERM itself (orderly shutdown, exit codes 0 to 6 in `docs/operations/local-server.md` section 8); no init wrapper is needed.
- **Health:** no `HEALTHCHECK` instruction; Kubernetes probes `/livez` and `/readyz` on the player port.

## Reproducibility

`-trimpath -buildvcs=false -ldflags '-s -w -buildid='`, `CGO_ENABLED=0`, `GOFLAGS=-mod=readonly`, `go mod verify` before compilation, digest-pinned bases. Image-level reproducibility (identical image digest from identical inputs) additionally needs the CI job to pass `SOURCE_DATE_EPOCH` and `--provenance=false --sbom=false` or an equivalent rewrite step; that is specified with the CI job, not here.

## Unverified until the CI build exists

- That the Dockerfile builds at all (BuildKit syntax, `COPY --chown` on distroless, `go mod verify` under `-mod=readonly`).
- That the resulting binary starts as 65532 with a read-only root and a mounted volume (the kind job's first assertion).
- That `kubectl port-forward` reaches the admin listener inside the pod (see `docs/operations/storage-and-network-validation.md`).
- Image size, Trivy result, SBOM.
