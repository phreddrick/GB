# ADR-0006: Container images: GHCR, digest pinning, distroless non-root, supply-chain checks

Status: Proposed 2026-10-30 (Phase 2B draft for owner review; `deploy/docker/Dockerfile` written, never built)

## Context

The root `Dockerfile` that exists today builds `makeuniv`, `gb-server` and `racegen` on a digest-pinned Go builder but runs them as root on an unpinned `alpine:latest`, copies the whole repository (including `source/`) into the build context, and does not build `gbctl`. CI builds it without pushing; the workflow has `contents: read` only. No `ghcr.io/phreddrick/gb*` package exists. The repository is public. The development host runs no container runtime by policy, so images can only be built in GitHub Actions. The migration plan (11.2, 13.1) asks for one multi-stage Dockerfile with one target per binary on distroless, digest-pinned bases, digests in manifests, GHCR, scanning and a digest-bump pull request.

## Decision

1. **One Dockerfile, `deploy/docker/Dockerfile`, context `go/`.** Targets `server` (`gb-server`) and `gbctl`. Builder `golang:1.27.3-bookworm`, runtime `gcr.io/distroless/static-debian12:nonroot`, both referenced by their image-index digests as read from the registries on 2026-10-30 and recorded with the retrieval commands in `deploy/docker/README.md`. A digest is bumped only by a reviewed commit that updates that table.
2. **Non-root, static, shell-less.** `CGO_ENABLED=0`, `GOTOOLCHAIN=local` with a go1.27.3 assertion, `GOFLAGS=-mod=readonly`, `go mod verify` before compiling, `-trimpath -buildvcs=false -ldflags '-s -w -buildid='`; `USER 65532:65532` repeated in the final stages; `ENTRYPOINT` on the binary; no package manager, no shell, no `HEALTHCHECK`. The only writable location at runtime is the mounted volume (`GB_DB_PATH`); the root filesystem is read-only in the manifests.
3. **Registry and naming.** `ghcr.io/phreddrick/gb-server` and `ghcr.io/phreddrick/gbctl`, tagged `<git-sha>` and `master` for humans; **manifests reference digests only**. The CI publish job (next stage) runs on pushes to `master` with job-scoped `packages: write` and prints the digests; a separate job opens the digest-bump pull request against `deploy/kustomize/apps/dev/server/kustomization.yaml`, which a human merges. Until the first publish the dev entry point carries the documented all-zero sentinel, which the validator reports and `--require-published-image` rejects.
4. **Pull access.** Public packages, no `imagePullSecret` (plan Decision 6, repository already public). GitHub creates a new package as private; making it public is a one-time owner action in GitHub settings recorded in `docs/operations/bootstrap.md`. If the owner keeps it private, an `imagePullSecret` becomes a third out-of-band Secret.
5. **Supply chain in CI.** `govulncheck` (already), Trivy on both images failing on critical fixable findings, an SBOM attached to the image; cosign keyless signing deferred (Decision 6). `linux/amd64` only.
6. **The legacy root `Dockerfile` stays until the CI `docker` job is retargeted** in the next stage, then becomes a pointer; the `KUBERNETES.md`/`k8s-deployment.yaml` pair already carries superseded notices.

## Alternatives considered

- Keep alpine with a `USER` line: smaller change, but a shell and package manager in the runtime image, `latest` drift and a CA bundle the server does not need; rejected.
- `scratch` base: works for a static binary, but distroless/static adds `/etc/passwd` for `runAsNonRoot` name resolution, CA certificates and tzdata at negligible size; chosen over scratch.
- Tag-based deployment with `imagePullPolicy: Always`: rejected; a retargeted tag changes what runs without a commit.
- Docker Hub or a self-hosted registry on the QNAP: rejected; GHCR is free for a public repository and needs no new credential in the cluster.

## Consequences

- The Dockerfile is **unverified until the CI job runs it**: BuildKit syntax, `COPY --chown` on distroless and the module download step are checked statically only. The handoff says so.
- Operators cannot `kubectl exec` into the pod (no shell); debugging uses `kubectl debug` with an ephemeral container, and `gbctl` runs from a workstation through `kubectl port-forward` or as a one-off `kubectl run` of the `gbctl` image.
- `GB_ADDR=0.0.0.0:8080` is baked into the image because the code default is unreachable from outside a container; the admin bind stays a deployment decision.

## Validation

CI (next stage): both targets build; `docker inspect` shows `User: 65532:65532`; the kind job starts the image with `readOnlyRootFilesystem: true` and a mounted volume and reaches `/readyz`; Trivy and SBOM steps pass; the published digest matches the one the digest-bump pull request writes. Locally: `deploy/kustomize/tests/validate.py` enforces the digest form of the image reference.

## References

Migration plan 11.2, 13.1, 16.4 Phase 2, Decision 6; `deploy/docker/README.md`; Phase 2A assessment section 8; ADR-0004.
