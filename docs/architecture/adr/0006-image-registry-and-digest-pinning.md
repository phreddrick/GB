# ADR-0006: Container images: GHCR, digest pinning, distroless non-root, supply-chain checks

Status: Proposed 2026-10-30 (Phase 2B draft for owner review; `deploy/docker/Dockerfile` written; Phase 2C added the CI build, scan, SBOM, kind, publish and digest-update workflows as artifacts, see "Phase 2C amendments" below; no hosted run and no publication yet at the time of writing)

## Context

The root `Dockerfile` that exists today builds `makeuniv`, `gb-server` and `racegen` on a digest-pinned Go builder but runs them as root on an unpinned `alpine:latest`, copies the whole repository (including `source/`) into the build context, and does not build `gbctl`. CI builds it without pushing; the workflow has `contents: read` only. No `ghcr.io/phreddrick/gb*` package exists. The repository is public. The development host runs no container runtime by policy, so images can only be built in GitHub Actions. The migration plan (11.2, 13.1) asks for one multi-stage Dockerfile with one target per binary on distroless, digest-pinned bases, digests in manifests, GHCR, scanning and a digest-bump pull request.

## Decision

1. **One Dockerfile, `deploy/docker/Dockerfile`, context `go/`.** Targets `server` (`gb-server`) and `gbctl`. Builder `golang:1.27.3-bookworm`, runtime `gcr.io/distroless/static-debian12:nonroot`, both referenced by their image-index digests as read from the registries on 2026-10-30 and recorded with the retrieval commands in `deploy/docker/README.md`. A digest is bumped only by a reviewed commit that updates that table.
2. **Non-root, static, shell-less.** `CGO_ENABLED=0`, `GOTOOLCHAIN=local` with a go1.27.3 assertion, `GOFLAGS=-mod=readonly`, `go mod verify` before compiling, `-trimpath -buildvcs=false -ldflags '-s -w -buildid='`; `USER 65532:65532` repeated in the final stages; `ENTRYPOINT` on the binary; no package manager, no shell, no `HEALTHCHECK`. The only writable location at runtime is the mounted volume (`GB_DB_PATH`); the root filesystem is read-only in the manifests.
3. **Registry and naming.** `ghcr.io/phreddrick/gb-server` and `ghcr.io/phreddrick/gbctl`, tagged `<git-sha>` and `master` for humans; **manifests reference digests only**. The CI publish job (next stage) runs on pushes to `master` with job-scoped `packages: write` and prints the digests; a separate job opens the digest-bump pull request against `deploy/kustomize/apps/dev/server/kustomization.yaml`, which a human merges. Until the first publish the dev entry point carries the documented all-zero sentinel, which the validator reports and `--require-published-image` rejects.
4. **Pull access.** Public packages, no `imagePullSecret` (plan Decision 6, repository already public). GitHub creates a new package as private; making it public is a one-time owner action in GitHub settings recorded in `docs/operations/bootstrap.md`. If the owner keeps it private, an `imagePullSecret` becomes a third out-of-band Secret.
5. **Supply chain in CI.** `govulncheck` (already), Trivy on both images failing on critical fixable findings, an SBOM attached to the image; cosign keyless signing deferred (Decision 6). `linux/amd64` only.
6. **The legacy root `Dockerfile` stays until the CI `docker` job is retargeted** in the next stage, then becomes a pointer; the `KUBERNETES.md`/`k8s-deployment.yaml` pair already carries superseded notices.

### Phase 2C amendments (2026-10-30, `docs/testing/phase2c-handoff.md`)

- **Decision 3, trigger:** publication does **not** run on pushes to `master`. `publish-images.yml` is `workflow_dispatch` only, from `master`, with a typed confirmation and the environment `ghcr-publish`, and requires the `ci` checks of the exact commit to have succeeded. Automatic publication on master is migration plan Decision 31, to be taken after the first publications. The digest-bump pull request is `digest-update-pr.yml`, called from the publish run on request or dispatched by hand; it never merges.
- **Decision 3, digest identity:** builds run with `provenance=false`, `sbom=false`, one platform, so each published artifact is a **single `linux/amd64` OCI image manifest** (no index). That manifest digest is what buildx writes into the OCI archive, what Trivy scanned, what `crane push` uploads unchanged, and what the Kustomize entry point references. Tags `<sha>` and `master` are convenience only.
- **Decision 5, SBOM:** the SBOM (Syft, SPDX JSON, one per image) is kept as a 90-day workflow artifact with the publish record, not attached to the image as a referrer; attaching, provenance attestations and cosign signing stay deferred (Decision 6).
- **Decision 5, scan policy as code:** Trivy (pinned) scans both final images; `.github/scripts/trivy_policy.py` fails on a CRITICAL vulnerability with a fix available or on any secret and reports every other finding. The scan runs in `ci` and again in the publish job before anything is pushed.
- **Reproducibility, stated exactly:** source builds are repeatable (pinned toolchain, `go mod verify`, `-trimpath -buildid=`), `SOURCE_DATE_EPOCH` is the commit time and layer timestamps are rewritten, so an identical image digest from an identical commit is *expected* and is recorded by comparing the `ci` and publish records; it is not asserted and is not claimed until observed.

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

CI (Phase 2C workflows, pending their first hosted run): both targets build; `.github/scripts/image-checks.sh` asserts `User 65532:65532`, static binaries from the pinned toolchain, no shell, distinct images, a read-only start on a tmpfs volume and refusal without secrets; the kind job starts the image under Pod Security `restricted` with `readOnlyRootFilesystem: true` and a mounted volume and reaches `/readyz`; Trivy and SBOM steps pass; `publish-push.sh` fails unless the published digest equals the scanned archive's and a validated publish record exists (a half-published pair is `partial`, never a release); `trivy_policy.py` accepts only a well-formed Trivy report about the built archive; `verify-published-image.sh` ties a proposed digest to its source commit through the revision label before the digest-bump pull request is opened. Locally: `deploy/kustomize/tests/validate.py` enforces the digest form of the image reference and `.github/scripts/workflow_policy_check.py` keeps the permission model.

## References

Migration plan 11.2, 13.1, 16.4 Phase 2, Decision 6; `deploy/docker/README.md`; Phase 2A assessment section 8; ADR-0004.
