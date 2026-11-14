# PR78 artifact download HTTP 415 correction

Baseline: `e70148ea1e9cb0c3681d32eb4f0abae59f473322`.
Diagnostic [PR #81](https://github.com/phreddrick/GB/pull/81), HEAD
`ca6daf4dc120c40f7dc9ddd14dc2041bab8da32c`, remains unchanged.
Its [read-only run 43555882015](https://github.com/phreddrick/GB/actions/runs/43555882015)
returned HTTP 200 for CI/jobs and all four artifact metadata requests, then
HTTP 415 for all four downloads before any redirect. This reproduces an API
negotiation defect; it is not evidence of a storage outage. The original failed
promotion run `43550571185` did not retain its HTTP status and remains preserved.

## Production correction

`API.get` previously equated `binary=True` with octet-stream negotiation.
Actions artifact ZIP endpoints instead negotiate GitHub JSON at the API and
redirect to raw ZIP bytes. The correction selects octet-stream only for binary
`releases/assets/` requests and GitHub JSON for Actions/metadata requests.
`binary` still controls raw response handling; ZIP bytes are never JSON-decoded.

Reference: [GitHub artifact download API](https://docs.github.com/en/rest/actions/artifacts#download-an-artifact).
Release assets retain their separate binary negotiation for direct HTTP 200
and HTTP 302 responses. No API-version, token, timeout, download bound,
redirect destination/TLS/port validation, Authorization stripping, error
redaction, checksum, archive, OCI or provenance rule changes are made.
`publish-images.yml`, `pr78_promotion.py` and the fixed PR78 profile are unchanged.

## Permanent regression and hosted proof

Five new header/transport tests drive the production OpenerDirector and
Redirects through a mock HTTPS transport, with no real network or credentials:

- All four Actions IDs use JSON Accept, follow a real urllib 302 and return
  non-JSON ZIP bytes unchanged.
- Release assets use octet-stream for both direct and redirected bytes.
- Redirected requests contain no Authorization header.
- HTTP 415 remains redacted and causes no retry/fallback.
- Size overflow and unsafe HTTP/host/port/userinfo redirects remain refused.

The unchanged master client fails the new four-ID regression with HTTPError;
the corrected client passes. The pre-fix failure is preserved separately.

For live read-only proof this PR reuses PR #81's reviewed diagnostic harness
without modifying that PR or its worktree. The dedicated PR-triggered job has
exactly contents/actions read, uses only its automatic token, requires a
same-repository PR and probes the fixed original CI and four artifact identities.
The observer does not override the corrected production request configuration.
It validates complete ZIP size/hash and provenance, keeps bytes in memory,
and logs only fixed identifiers/categories, statuses, success and numeric quota
fields. It neither extracts nor publishes images. Paths additionally include
the production helper and its new header tests so future changes are checked.

The ten diagnostic tests retain their assertions; their Actions Accept
expectation is updated to JSON. The exact read-only job permission tuple is
the only static-policy addition; publisher policy and permissions are unchanged.
There is no publisher dispatch, GHCR write permission or release creation.

## Evidence and review

New protected evidence is kept outside Git at
`/home/steve/gb-evidence/pr78-artifact-accept-fix-20261114T044337Z/`.
It records pre-fix failures, final source hashes, actual local commands, exit
codes, outputs, independent review and hosted read-only observations. No prior
promotion or diagnostic evidence is amended. The PR job log and PR description
record the hosted proof against the exact PR HEAD; local mocks alone do not
establish successful authenticated downloads.

Local acceptance covers helper/promotion regressions, policy, actionlint,
ShellCheck, build/format/vet, module integrity, C fixtures, normal tests and
inventory, race tests, all 15 mutations, strict govulncheck, lint, gate self-tests
and offline manifests. Actual results and timings belong to the evidence ledger;
no test, gate or timeout is weakened. Independent review covers the production
change, retained-asset behavior, credential boundaries and verification workflow.

Required maintainer-applied acknowledgment: **gate-change-reviewed**. No lint
baseline or known-defect allowance changes are included. No label is applied by
the agent. This PR does not authorize promotion, image publication, merge,
deployment, live database access or cluster operations.
