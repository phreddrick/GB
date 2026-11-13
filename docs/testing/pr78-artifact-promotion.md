# PR #78 exact-artifact promotion

This change adds an explicit `promote-pr78` operation to the existing manual
`publish-images.yml`. It implements a publication mechanism, **not authorization
to run it**. No image was published during implementation. Deployment, quiesce,
recovery-point creation and resume remain separate owner decisions.

## Fixed release and control identities

The release source is `7a17a1bc7edf75091386f30944a529a0ccf48d8d`, original master
push CI run `43451208400`, attempt **1**, all seven jobs successful. The server
manifest is `sha256:66b72b138dbeff1b79044f44548adef3dddc476e35b1e3d0deb9ee9db73da96a`;
gbctl is `sha256:0b388843751003312da6dee65672359d47a8bb06fbba7ce8037ae1de72dafedb`.
The ZIP digest `09cc296ced40ead00aaabc4044409d1fee36db1faa51bf2134523efb3a0ddd1c`
is an artifact identity, **not** either image digest.

The checked-in `.github/release/pr78.json` fixes all four original artifact IDs,
ZIP hashes/sizes/entry sets, image manifests/configs and OCI/Docker TAR hashes.
There is no caller-supplied source, digest, image namespace or download URL.
Current master includes PR #79; its game code must never supply these images.
Control workflow code is checked out from the dispatched master commit and must
itself have successful CI through the unchanged original preflight. The original
image-source CI is independently checked as well. Promotion records distinguish
control commit/run/attempt from original build commit/run/attempt; `GITHUB_SHA`
is never spoofed to the historical source.

## How the bytes reach Actions

Actions cannot read igor's evidence directory. With `retained_release_id` empty,
the read-only preflight downloads the four fixed GitHub CI artifacts directly,
rejecting missing, expired, wrong-source, wrong-size or wrong-hash objects. The
original `container-images` expires **2026-11-20T15:19:19Z**. Missing artifacts
never cause a rebuild, current-master substitution or implicit fallback.

After expiry, the explicit alternative is a separately owner-prepared,
published (not draft) GitHub **phreddrick/GB** retention release containing:

- `container-images.zip`
- `image-build-record.zip`
- `trivy-reports.zip`
- `sboms.zip`

The owner transfers these exact preserved files from
`~/gb-evidence/pr78-artifact-preservation-20261113T204857Z/` to GitHub using an
already authorized owner session/UI or authenticated CLI. That upload and any
release/tag creation require separate authorization and are **not** performed
by this mechanism or this PR. An existing owner-approved retention release may
be used; never clobber assets or commit archives to Git. Obtain its numeric
release ID from GitHub metadata. The release's target commit is not evidence of
image source: original CI, pinned ZIPs and OCI labels establish that identity.

Both release author and asset uploader must be `phreddrick`, immutable account
ID `1794145`; the four asset names must each occur exactly once with the pinned
size/hash. The workflow reads them with its existing `contents: read` permission,
fully paginates the asset list within a bound, and rechecks bytes. It neither
creates a release nor accepts arbitrary URLs. Fresh original CI run/job metadata
is still mandatory in this mode. If that evidence is gone, stop; retained ZIPs
alone are not a substitute for CI provenance.

Preflight hands the original ZIPs plus sanitized provenance to the privileged
job through an artifact named with **this** workflow run and attempt. That job
uses only same-run download, independently checks all pins again, and never
loads or executes files from the artifact. ZIP paths/types and top-level TAR
members are checked before extraction; OCI metadata/blobs are materialized,
not image filesystems. All OCI descriptors, compressed blobs and ordered
uncompressed DiffIDs, both Docker archives, build record, revision labels,
linux/amd64, nonroot identity and original SPDX source bindings are validated.

## Operator selection and permission boundaries

Only after review, merge, successful control-code master CI and separate owner
publication authorization, select the existing manual workflow on **master**:
`operation=promote-pr78`, `confirm=promote-pr78-exclusive`,
`open_digest_pr=false`, and optionally the approved `retained_release_id`.
The confirmation attests an effective exclusive registry-writer window. Stop
before dispatch if another writer cannot be excluded. Repository/owner IDs,
master ref, workflow identity and manual event are checked again in the helper.

The existing `ghcr-publish` environment, shared `ghcr-publish` concurrency group,
`cancel-in-progress: false`, pinned actions/tools and **one** package-writing
job remain. Preflight retains only contents/actions read; publishing retains
only contents read/packages write. No PAT or new secret is introduced. Actual
environment reviewer rules and package access remain owner-controlled settings
to verify before any authorized execution; this PR does not change them.

The default `build-current-master` operation preserves the old `publish`
confirmation and normal build/publish semantics. Retention input in that mode
is rejected. Promotion skips Buildx/game builds, the old master-tag publisher,
and the optional digest-PR job. Requesting an automatic digest PR in promotion
mode is rejected. The existing `publish_record.py` contract is unchanged.

## Fresh security evidence

Promotion runs the pinned Trivy actions against the verified OCI layouts with
all severities, vulnerabilities and secrets enabled. The existing policy is
unchanged: malformed/wrong-image reports, fixable CRITICAL or any secret fail.
The promotion helper repeats that policy before its first write and requires
report timestamps inside this run's bounded scan window and the pinned scanner
version. A fresh scan is required even though historical reports passed.
Historical reports contain HIGH findings; policy PASS is not vulnerability-free.

After each scan, the promotion-only helper records the actual scanner cache's
DB schema/timestamps plus database and metadata SHA-256. The cache path/JSON
fields were checked against the pinned Trivy action and its pinned trivy-db
source. Overdue DB metadata, unavailable evidence, unexpected version or links
fail closed. The scanner version file, reports and DB evidence are retained.
The original SPDX SBOMs are reused because the image bytes are unchanged and
source-manifest bindings are verified; their original ZIP hash is in the record.
No local Trivy execution, container runtime or image execution on igor is claimed.

## Registry writes, uncertainty and records

Only the two exact `<approved-source>` image tags can be written. There is no
master-tag writer or manifest-editing function. Both existing destination tags
are checked before either upload; a different existing digest is refused. An
identical existing image is verified without rewriting its tag. Before/after
master digests must match. Shared workflow concurrency and operator exclusivity
are prerequisites: registry tag reads are not atomic compare-and-swap, and
before/after equality cannot exclude another writer's intervening/ABA change.

Each write attempt is journaled and fsynced **before** invocation. A nonzero,
timeout or interruption after invoking upload is attempted/unknown, never proof
that nothing reached GHCR. Both repositories cannot be published atomically.
A failed second upload preserves the verified first image and reports partial;
there is no automatic retry, tag rollback, deletion or exactly-once guarantee.

Post-upload verification retrieves each image by immutable digest as a complete
OCI layout and verifies manifest/config/blob/DiffID integrity and identity again.
The separate closed `gb-pr78-artifact-promotion-v1` record stores source/original
CI, control identity, transport, local/observed manifests/configs, fresh scan/DB
and SBOM evidence, actual per-image write states and unchanged master tags.
Completion requires both images verified, unchanged masters and a validated,
no-replace, read-back final record. No old `published` record is fabricated.
The journal has a different kind, is explicitly provisional and cannot validate
as a complete promotion record if finalization fails. A confirmed upload still
stands after reporting failure; inspect evidence instead of blindly retrying.

The workflow retains input ZIPs/provenance, scan/DB evidence, original SBOMs,
record and journal for 90 days, including failure evidence. A later artifact
upload/job failure cannot undo registry effects. Abrupt runner loss may prevent
journal upload; never infer rollback from a missing Actions artifact. Operators
can validate a retained final record with `pr78_promotion.py verify-record`, but
must also verify its trusted workflow origin and remote identities.

No digest PR is opened by promotion. After a complete accepted result the owner
may separately authorize the existing digest-update workflow with source SHA
`7a17a1bc…` and the exact server digest, review/merge its focused PR, and inspect
the full rendered/live **image-only** Argo diff before any future sync. This
implementation does not authorize any of those operations. The earlier live
SERVING 248/0/693 observation and November 9 recovery point remain historical;
no live state was queried or changed here.

## Validation and independent review

Synthetic API/archive/registry tests exercise actual admission, scan, record and
writer code without network or container execution. Cases include tampered ZIPs,
unsafe entries, missing/mixed/failed CI, incorrect source/digests, expired or
unavailable original artifacts, explicit retained delivery and owner checks,
credential-stripping redirects, unauthorized tags, stale/failed scans, broken
remote retrieval, partial and unknown writes, master drift, finalization failure
and mode/permission/handoff boundaries. Real preserved ZIPs were also admitted
using the unmodified fixed production profile and fresh authenticated CI proof.

Independent review initially reproduced a blocking journal truthfulness defect
(R1): a finalization failure left a validator-accepted complete checkpoint. The
journal now has a distinct provisional contract and explicit finalization-failed
state; the regression checks that it cannot validate as a promotion record.
R2 requested complete config and DB evidence; both were added and tested. Initial
fixture-interface errors and review evidence are preserved, not rewritten.
Final results and hashed commands/source/output evidence are in
`docs/testing/evidence/pr78-promotion-validation.json`. Final review disposition
is recorded there after revalidation. Hosted publication and registry integration
remain unexecuted pending separate owner approval; no mock is presented as a real
publication.

Required maintainer acknowledgment: **gate-change-reviewed**. No lint baseline,
known-defect allowance, workflow security policy or existing publish-record
validator was weakened or changed; no `known-defect-added` label is required.
