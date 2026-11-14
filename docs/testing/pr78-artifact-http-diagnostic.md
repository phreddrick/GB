# PR78 read-only artifact HTTP diagnostic

The failed promotion run [43550571185](https://github.com/phreddrick/GB/actions/runs/43550571185)
lost its failing endpoint and status when `pr78_artifacts.API.get` reduced an
HTTPError to its class name. The prior evidence assessment remains unchanged:
it does not establish an authentication, negotiation, expiry, redirect or
service-outage cause. This PR collects new attributable observations; it cannot
retrospectively prove an intermittent historical cause.

## Execution and authority

`pr78-artifact-diagnostic.yml` runs only on opened/synchronized/reopened PRs
touching its three diagnostic files, and its one job requires a same-repository
PR in `phreddrick/GB`. Python independently checks repository/owner IDs, PR
event, head/base repository IDs and the fixed source/run/artifact mapping.
The job has exactly `contents: read` and `actions: read`, with the automatically
issued GITHUB_TOKEN supplied to the same GH_TOKEN environment variable the
production API client uses. Checkout does not persist credentials.

The sole policy allowlist addition permits those two read scopes for this exact
workflow/job tuple. All other permission, trigger, action-pin, publisher and
secret rules remain intact. No PAT, additional secret, environment, registry
login, package permission, artifact upload or dispatch trigger is present.

## Fixed requests and transport

The diagnostic makes ten GET operations once: original CI run `43451208400`,
attempt-1 jobs, and metadata/download for `container-images` (14049614280),
`image-build-record` (14050462489), `trivy-reports` (14051100529) and `sboms`
(14050950671). Individual failures do not hide the remaining fixed probes.
There are no automatic retries, retained-release alternatives or arbitrary
source, endpoint or artifact inputs.

It invokes the unchanged production `API.get` and inherits production redirect
validation. HTTP method, Accept/API-version/Authorization headers, default
User-Agent, 60-second socket timeout, HTTPS host restrictions, three-redirect
limit and redirect credential stripping remain the same. An observer records
HTTP status before the production exception redaction. A response wrapper
distinguishes interrupted reads without changing the byte limit. The job's
15-minute timeout bounds the complete diagnostic.

Metadata is bounded to 4 MB, and ZIP reads use each pinned original size plus
the production one-byte overflow check. ZIP size/hash and original run/job and
artifact provenance validators are retained. Downloaded bytes remain in memory
only; they are never extracted, loaded, executed, written or published.

## Output and interpretation

Each diagnostic JSON record contains only a fixed operation identifier, numeric
HTTP status (or null), fixed failure category, success Boolean and allowlisted
numeric rate-limit/reset/retry values. Intermediate permitted redirects have a
fixed `-redirect` operation suffix. No URL, redirect query, response body,
Authorization value, raw header collection or exception text is logged.

401/403 indicate access restrictions, except 403 with exhausted quota or numeric
Retry-After is classified as rate limiting. 404/410 indicate unavailability;
415 indicates negotiation; 429 indicates throttling; 5xx indicates a possible
service failure. Redirect refusal/failure, network interruption and local
response/provenance/integrity refusal have separate categories. Successful
current probes suggest a nonpersistent historical failure but do not prove
which service or request failed previously. No Azure outage is assumed.
An HTTP 200 with `response_validation_failed` means the request returned a
response but a local validator refused it. In particular, failed run metadata
also prevents subsequent job-provenance validation, even if the jobs GET is
HTTP 200. The diagnostic's PR token has the same requested scopes and token
class as the publisher's preflight, but is a new token in a different event;
it is not a replay of the historical ephemeral token.

## Tests, evidence and merge boundary

Ten focused tests cover status categories, rate-field sanitization, secret-
bearing errors/URLs/bodies, production transport equivalence, bounded reads,
interrupted reads, redirect authentication stripping and refusal, same-repository
authority, fixed identities, all ten probes without retries and workflow scope.
All 226 helper tests, 19 policy checks, actionlint, CI-level ShellCheck and gate
self-tests passed locally on the diagnostic source. The real growth gate
returns 1 until the maintainer acknowledges the protected tooling changes.
No lint baseline or known-defect allowance changes are included.

Local execution logs and hashes are preserved outside Git under
`/home/steve/gb-evidence/pr78-http-diagnostic-20261114T041010Z/`.
The PR's diagnostic job log supplies new hosted observations; no original
historical report is amended. Independent source/security review is required
before delivery; its final result is recorded in that evidence directory.

Required maintainer label before any later merge: **gate-change-reviewed**.
Do not merge merely to run this PR-triggered diagnostic. No merge, publication,
deployment, Kubernetes, credential rotation or live-database operation is
authorized by this diagnostic.
