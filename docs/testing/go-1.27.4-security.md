# Go 1.27.4 security maintenance

This standalone maintenance change starts from master
`51fd2b45658e28e580e6df70009984149f9bf561`. It addresses the Go standard-library
finding GO-2026-6859 (`net/http`, fixed in Go 1.27.4), reported by the strict
scanner in [PR #35's hosted run](https://github.com/phreddrick/GB/actions/runs/42956871415).
It contains no CR-17C factory implementation or game/storage changes.

## Pins and integrity

The module's default toolchain, all three workflows, both Docker builders and
their existing exact version assertions now use **Go 1.27.4**. The module's
`go 1.25.0` language minimum is unchanged. `GOTOOLCHAIN=local`, all seven CI jobs,
image identity/provenance validation, reproducibility flags, strict govulncheck,
Trivy and Syft, and manual/environment-gated publishing remain unchanged.
Existing `GOTOOLCHAIN=auto` exceptions for pinned external tool installation are
unchanged; application build/test/scanning stays local to the selected toolchain.

[Verified pins](evidence/go-1.27.4-pins.json) record both Docker Hub image-index
and linux/amd64 manifest/config digests. The builder table and retrieval commands
are also in [deploy/docker/README.md](../../deploy/docker/README.md).

On 2026-11-11 UTC, read-only HTTPS requests to Docker Hub obtained an anonymous
pull token (not retained), then fetched `library/golang` manifests for
`1.27.4-bookworm` and `1.27.4-alpine`, accepting OCI/Docker index media types.
For each:

1. SHA-256 of the exact index response matched `Docker-Content-Digest`.
2. The index's linux/amd64 descriptor identified the separately fetched manifest;
   its response header, byte hash and size agreed with the descriptor.
3. The config blob hash and size matched its manifest descriptor; OS/architecture
   were linux/amd64 and config environment declared `GOLANG_VERSION=1.27.4`.

The official [Go download metadata](https://go.dev/dl/?mode=json) supplied the
70,680,823-byte `go1.27.4.linux-amd64.tar.gz` SHA-256
`7226736c2339719cda28fbf3a54d71aa63bcefe376e98cef00034268dab24840`.
Both size and hash were verified before unpacking into a private `/tmp` directory.
`GOTOOLCHAIN=local go version` there reports `go1.27.4 linux/amd64`; the system
Go installation was not modified. No container image was built or run locally.
Hosted Docker checks must independently verify the actual compiled binaries.
Historical dated test evidence still identifies its original Go 1.27.3 runs.

## Local validation

Fresh commands use the verified temporary Go binary first on PATH,
`GOTOOLCHAIN=local`, `GOCACHE=/tmp/cr07a-cache`, and a private TMPDIR inside this
worktree's ignored `test/ctool/obj/tmp`. No tests use live stores.

Results and output hashes are recorded in
[the execution ledger](evidence/go-1.27.4-validation.json).

| Check | Fresh result |
| --- | --- |
| Build, gofmt, vet | PASS |
| Normal uncached full Go suite | 343 top-level passes / 3,353 passing nodes; two opt-in long tests skipped |
| Full `GB_LONG_TESTS=1` uncached race suite | 345 top-level passes / 3,355 passing nodes; zero test skips/failures |
| Strict govulncheck v1.9.0 on Go 1.27.4 | PASS; zero reachable vulnerabilities; 17 non-reachable module findings remain |
| Same scanner on system Go 1.27.3 | Expected failure: GO-2026-6859; found net/http@go1.27.3, fixed in go1.27.4 (inner scanner exit 3, `go run` exit 1) |
| Workflow helper tests / policy | 188 tests, including four new toolchain tests; 17 policy checks PASS |
| actionlint / exact CI ShellCheck policy | PASS; `shellcheck -S warning` unchanged. An additional default-severity run reports two pre-existing SC2015 informational diagnostics in image-checks/publish-push |
| Offline manifests | PASS; 21 resources valid, no invalid/error/skipped resources |
| Original C fixtures / all 15 golden mutations | PASS |
| Known-defect reconciliation / lint | PASS; existing baseline unchanged, 1,010 findings matched, zero new/stale |
| Approval, lint and mutation-helper self-tests | PASS |
| Growth gate | Expected refusal without label; PASS with locally simulated `gate-change-reviewed`; no GitHub acknowledgment applied |
| Module verification / tidy diff / working and staged diff checks | PASS |

The full suites include existing enrollment, turn/segment/shutdown atomicity,
maintenance, command persistence and real CLI/HTTP/WebSocket regressions. Six
packages have no test files; those package-level records are separate from the
zero skipped tests in the long race run. Docker builds, container identity,
Trivy, Syft and kind execution remain hosted CI work.

Independent review returned **APPROVE WITH CONDITIONS**, with no blocking finding.
The reviewer independently fetched and hashed both registry indexes and checked
the full amd64 descriptor/config chains, official archive checksum, and temporary
Go version. Parsed YAML comparisons prove that all three workflows are identical
to baseline except `env.GO_VERSION`. Fresh independent execution passed all 188
helper tests, four focused pin tests, 17 policy checks and diff checks. Review
suggestions corrected the README table placement and ensured a truncated evidence
list cannot silently omit a Docker builder from validation. Conditions are
maintainer acknowledgment, normal owner review and hosted CI. The final reviewer
recomputed all 13 frozen-file hashes and 24 execution-log hashes and verified
the full normal/race test counts and all 15 mutation scenarios.

## Review, acknowledgment and handoff

The new workflow regression tests check the module minimum/default, all workflow
Go pins/local policy/setup-go settings, both Docker pins/assertions against the
verified evidence, the seven-job set and unchanged strict scanner/image checks.
Workflow policy, triggers, permissions, scans and publishing restrictions are not
relaxed. Runtime images and Kubernetes manifests are unchanged.

**Maintainer-applied `gate-change-reviewed` is required** for the workflow and
protected script/test changes. The implementation agent does not apply it.
There is no lint-baseline, golden allowance or known-defect inventory change in
this maintenance PR. PR #35 independently still requires `known-defect-added`
for its two removed lint findings.

After normal review and maintainer merge of this maintenance PR, update PR #35
against the patched master and rerun hosted CI. This task does not modify that
worktree or branch, merge either PR, publish images or deploy. No Docker/kind
installation, live database, Kubernetes, Argo CD, Longhorn or live-game operation
is authorized or performed.
