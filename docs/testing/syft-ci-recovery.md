# Bounded Syft installation recovery

Baseline: `0728db3ae96e602d3890656655c1053c0269dee1`.
[CI run 42581835532](https://github.com/phreddrick/GB/actions/runs/42581835532)
failed before SBOM generation: the Syft v1.55.0 release checksum download
returned HTTP 504. All six other jobs passed.

## Recovery and integrity

Only `image-sbom` changes. It retries the same pinned
`anchore/sbom-action/download-syft@66cbf4bc1f1c0d2edc94016e65bc221b6bb0ad6c`
(v0.24.3), still requesting Syft `v1.55.0`. There are at most three installation
attempts, each with a two-minute step timeout, with 15/30-second delays before
attempts 2/3. The existing 15-minute job timeout remains. All installer failures
are eligible for bounded retry; no alternate download or unverified fallback is
introduced. Permanent failures remain failures.

The first two steps may recover with `continue-on-error`; retry conditions read
`steps.<id>.outcome`, which retains the original failure, rather than the
post-recovery `conclusion`. The final attempt cannot continue on error. Normal
success gates remain on scanning/upload, so exhaustion fails the job rather
than reporting success without SBOMs. Cancellation is not overridden. Command
selection accepts output only from an attempt whose outcome is success.
See [GitHub's steps context](https://docs.github.com/en/actions/reference/workflows-and-actions/contexts#steps-context).

The inspected [pinned action](https://github.com/anchore/sbom-action/blob/66cbf4bc1f1c0d2edc94016e65bc221b6bb0ad6c/src/github/SyftGithubAction.ts)
uses the requested release's installer, prohibits its unpinned self-redownload
for release tags, and caches the executable only after successful installation.
The [v1.55.0 installer](https://github.com/anchore/syft/blob/v1.55.0/install.sh)
requires the release checksum file and verifies the archive using
`hash_sha256_verify` before extraction. Each retry retains that verification.
This preserves release checksum verification; no new signature-verification
claim or distribution trust is introduced.

Both original OCI archives remain the inputs. Existing archive/digest validation,
source identity, version assertion, scans of `gb-server` and `gbctl`, nonempty
SPDX JSON checks and 90-day SBOM artifact retention are unchanged. No other job,
workflow security rule, permission, action pin, image provenance check, game
code, enrollment/storage behavior or Kubernetes resource changes.

## Verification

Four workflow regression tests cover the common action/release pin, bounded
retry/timeout conditions and fatal exhaustion, success-only command selection,
and mandatory verified image inputs/both SPDX outputs/artifact retention.
Existing workflow-policy rules remain unchanged.

Local results: all 184 Python helper tests passed, including the four new Syft
checks; workflow policy passed 17 assertions. Actionlint, workflow-script
ShellCheck, diff checks and offline manifest/safety checks passed (21 schema-valid
resources). The growth gate passed with a locally simulated acknowledgment and
correctly refused without it; no GitHub label was applied. Structural comparison
against the baseline confirmed every unrelated job/global setting, the original
SBOM scan script and both artifact steps are unchanged.

Independent review: **APPROVE**, no blockers. The reviewer independently ran 70
helper/workflow tests, 17 policy assertions and structural comparisons, verified
upstream checksum/cache behavior, and checked GitHub runner timeout/outcome
semantics. Hosted CI and maintainer acknowledgment remain required.

Actual GitHub runner execution and real image scanning remain hosted-CI checks;
local validation does not install Docker/kind or claim to reproduce GitHub's
transient 504. Nothing is merged, published, deployed or run against a live game.
Required maintainer-applied PR acknowledgment: **`gate-change-reviewed`**. The
implementation agent does not apply it; no allowance or baseline changes need
another acknowledgment label.
