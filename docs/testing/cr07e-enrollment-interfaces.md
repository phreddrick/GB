# CR-07E: private operator enrollment and secure login

Baseline: `fc5688ced0ac998cf77f192027db90536597d3fb` (CR-07D).
Implementation is for normal PR review and hosted CI. No deployment, image
publication, Kubernetes operation or live-universe access is authorized or done.
Public registration remains disabled, including when `GB_ENROLL_PREVIEW=true`.
The owner authorized this work ahead of broader Phase 3c.

## Operator boundary and architecture

The existing private administrative bearer authenticates **one configured
operator service principal**, identified by `operator_id`. It is not proof of an
ordinary player's identity, nor a separate identity service. All holders of that
bearer have the configured operator's authority; do not share it with players.
The protected server authority lists the accounts this operator may select,
immutable credential versions, and independent God-bootstrap/Guest permissions.
An account string or privilege boolean in JSON grants no authority by itself.

`POST /admin/v1/enroll` resolves that allowlisted binding, then invokes
`State.EnrollTrusted`. Only the existing actor constructs placement and commits
through CR-07C. There is no second writer, reservation transaction or service.
`Options.TrustedEnrollment` receives the production provider's bounded read-only
Authorize/Prepare callbacks; neither calls the actor recursively.

`GB_ENROLL_AUTHORITY_FILE` explicitly loads an owner-only regular file. With no
provider enrollment is denied, even with a valid admin token. Invalid/missing
artifacts fail startup; an enabled provider requires an admin listener and an
already initialized matching universe. The setting is environment-only and is
not accepted in ordinary JSON config. No deployment manifest enables it.

The game binding is `universe-` plus SHA-256 of the existing Go-serialized
`db.UniverseRecord`. It binds the seed, initialization time, generator and other
initialization metadata, survives turns/restarts, and cannot be changed to a
nickname. `gbctl enroll-game-id` reads authenticated status and computes it.
Startup and every authorization check this binding against the actual store.
Universe metadata is not rewritten. No new database key is required.

## Credential provisioning and immutable references

Use `gbctl credentials prepare -account ACCOUNT -race-file RACE_FILE
-governor-file GOVERNOR_FILE -out NEW_ACCOUNT_ARTIFACT` (one command). The
optional `--god-bootstrap` and `--allow-guest` flags describe provisioning
permissions; the protected server policy must still authorize them. This command
needs no server/admin token and performs **no enrollment**.

Password files and artifacts must be regular, non-symlink files with no
group/other permissions (normally 0600), in a trusted operator-owned directory.
Inputs are protected files, never password command-line flags or URL parameters.
The CLI removes one terminal line ending. Passwords must be distinct UTF-8
strings of 12..72 **bytes**, with no NUL or embedded line endings. Unicode byte
length matters for bcrypt. Provisioning hashes both independently at bcrypt's
default cost, propagates either hash error, and never falls back to plaintext.
The output is exclusively created with mode0600, synced and closed. Output and
errors contain neither passwords nor encodings. Protect/delete input files under
the operator's normal secret-management policy; do not commit real artifacts.

Merge the complete generated account objects into this protected authority
structure (placeholders below are not a runnable credential artifact):

```json
{
  "schema_version": 1,
  "operator_id": "private-operator",
  "game_id": "COPY_THE_UNIVERSE_BOUND_GAME_ID",
  "bootstrap_enabled": false,
  "accounts": [{
    "credential_version": "COPY_GENERATED_VERSION",
    "account_id": "account-001",
    "god_bootstrap": false,
    "allow_guest": false,
    "race_hash": "COPY_GENERATED_BCRYPT_ENCODING",
    "governor_hash": "COPY_INDEPENDENT_GENERATED_BCRYPT_ENCODING"
  }]
}
```

The manifest is trusted provisioning, not an untrusted credential upload route.
It validates canonical bcrypt encodings, independent representations and cost
10..14. Differently salted encodings cannot prove that handwritten secrets
differ: use the supported provisioning command. New dual login also refuses
identical supplied plaintexts. No hash/credential artifact is returned by HTTP.

`credential_version` content-addresses account + complete salted bcrypt pair.
It cannot be rebound: loading recomputes it. Server credential references also
bind game/account/kind and salted encodings, rather than fast hashes of plaintext.
The actor fingerprint contains these safe references, not bcrypt bytes/passwords.
The provider keeps independent immutable map copies of its configuration.

Retain historical account versions for retries and restarts. Adding a version
requires a new generated artifact and the same account privilege flags; removing
an old version disables retries selecting it. An already committed request with
changed credentials/version is a conflict. At most63 accounts and256 immutable
versions are accepted. Reload requires a separately authorized server restart;
there is no live credential registry or automatic reload. This provisioning unit
does **not** rotate an existing race's stored credentials: that needs a separately
reviewed account/credential lifecycle. Copies/clones of a universe retain its
identity and require operational isolation; this is not a global identity service.

## Requests, bootstrap and client behavior

`gbctl enroll -f operator-request.json [--god | --guest]` uses existing
`GBCTL_TOKEN`/`GBCTL_TOKEN_FILE` and private server/timeout/JSON conventions.
The request file is also required to be protected. It contains exactly:

- `request_id`: stable operator-assigned identifier, 1..64 ASCII letters/digits,
  underscore or hyphen. Never replace it after timeout or connection loss.
- `game_id`, `account_id`, `credential_version`: explicitly selected configured
  binding, with bounded IDs; no plaintext or client-computed request hash.
- `spec`: complete CR-07A JSON specification, including Fighters, abilities,
  homeworld and all eight sector compatibilities. Address/info are optional.
- `god`, `guest`: explicit booleans, mutually exclusive, server authorized.

All bodies are limited to65536 bytes. Exact fields and types are required;
unknown/duplicate properties (including nested), nulls, trailing JSON, nonfinite/
overflowing numbers and wrong sector-array lengths are rejected. CR-07A remains
authoritative for ranges, cross-field constraints, costs and the1400-point budget.
Names and privilege/account policy are validated separately by CR-07D.

The first newly created race must explicitly request God, as original C requires.
The account must have `god_bootstrap=true`, and `bootstrap_enabled=true` must be
explicitly configured server-side. Defaults deny both permissions. A subsequent
God creation is denied. Historical first-God retries remain recognizable after
disabling bootstrap. Guest has its own account permission and cannot also be God.
Existing universes/legacy races are never retroactively changed.

Responses project only player, home star/planet, capital coordinates, government
ship, creation time, replay and persistence outcome. HTTP200 alone is insufficient:
the client strictly validates the operation, complete result, binding echo, full
POST intent digest, enums and placement bounds. Truncated/unknown/duplicate JSON,
wrong identities, contradictory success/validation errors and malformed locations
cannot become success. Redirects are refused. Token/raw error bodies are never
echoed. Structured field validation is returned on authoritative design rejection.
A successful status query can return0 with NO_KNOWN_PLACEMENT; it does not
assert placement success. CLI codes remain0 success,1 usage,2 existing connection failures,3 token denial,
4 refusal,5 server/unknown outcome; enrollment transport ambiguity uses5.

## Status, timeout and reconciliation

`gbctl enroll-status -request-id ID -game GAME -account ACCOUNT` uses private
`GET /admin/v1/enroll/{request_id}?game_id=GAME&account_id=ACCOUNT`. The provider
authorizes the same game/account; no credential version is needed to inspect
historical placement. Status describes the original recorded placement, not a
modified request's success or the current survival of its government ship.

- CONFIRMED / COMMITTED: verified historical placement and actor/store agreement.
- NO_KNOWN_PLACEMENT / UNKNOWN: no record known; **not rollback proof**.
- CONFLICT: the ID belongs to another authorized caller's intent.
- NOT_PERMITTED: current admission/integrity preconditions prevent confirmation.
- RECONCILIATION_REQUIRED: halted/uncertain/inconsistent state; no safe retry.
- UNKNOWN: diagnosis unavailable, including unreadable/corrupt metadata.

`permitted` describes current enrollment admission, not success. New placements
and mutation retries still require SERVING and not draining. Read-only diagnosis
can report confirmed historical placement while QUIESCED; it cannot write there.
Queued cancellation means not executed. A started request can finish after its
HTTP/CLI wait ends; the buffered actor reply is retained and same-ID lookup/retry
can recognize the commit. The request fingerprint and CR-07C single transaction
are unchanged. Isolated crypto/rand permutations and the turn RNG are unchanged.

CR-07C uncertainty and CR-07D HALTED/no-shutdown-flush behavior remain intact.
Visible metadata after an error does not prove durability. There is no clear-
uncertainty/resume button or automatic reopen/retry with a new request ID.

Operator reconciliation runbook (diagnosis only, no repair authorized here):

1. Retain original request file/ID, authoritative account/game/credential version,
   bounded outcome code, process halt diagnostics and original store handle state.
2. Use the private status lookup. Treat UNKNOWN/absence as insufficient evidence.
   Stop sending writes; preserve evidence and do not restart to clear a guard.
3. Under a separate operational authorization, preserve an application-consistent
   recovery copy and compare strict placement metadata, account intent, complete
   race/star/planet/sector/ship records, orbital references and allocator evidence.
   A world digest alone omits placement metadata/free-list evidence.
4. Obtain independent operator review of durable state and the proposed recovery.
   Any repair, restore, reopen or resumption requires separate design/approval.
   This unit implements no reconciliation mutation or recovery automation.

## Login and session compatibility

Existing `POST /auth/login` accepts the additive strict contract:

```json
{"race":"Species","race_password":"...","governor":0,"governor_password":"..."}
```

Both credentials are checked independently, on the actor. No governor fallback,
empty governor credential, inactive governor, invalid index or mixed old/new
fields can authorize a new placement. New races require dual credentials based
on trusted strict CR-07 placement history, never client hints. Corrupt history
fails closed. CR-07B initializes active governor0; it does not activate governor1.

Legacy `{race,password,governor}` remains safe for historical races whose
existing credentials support it. Only legacy governor0 with an empty historical
hash can use its race hash; a configured governor hash must independently match.
No fallback enables another governor. Legacy users with distinct passwords can
use the additive dual contract. New placements cannot use legacy login.

Well-formed denied identities perform two dummy default-cost bcrypt comparisons,
and wrong credential responses are generic. Historical hash costs can still
vary; rate limiting/bcrypt offloading remain CR-20/public-access prerequisites.

JWTs retain the existing signing infrastructure with a credential/permission
revision and required expiry. WebSocket upgrade, actor welcome and each command
validate current race/governor/privileges, credentials, history, expiry and
revision. Existing sockets are revalidated at actor mutation boundaries and
before held turn notices; expired sockets cannot enqueue/deliver payloads.
Privilege removal, credential change, dissolution or governor deactivation revoke
old authority. JWTs lacking the new revision require a fresh login. No token
contains stored encodings. Query-string WS tokens remain existing CR-20 debt. The existing Go `gb-client`
still sends legacy login; new races need a dual-contract-capable client. Updating
that client and the future TypeScript clients remains acceptance/client work.

Saved-game format impact: **NONE beyond existing CR-07C placement metadata**.
No identity/auth-version keys, migration, CR-18 kind/schema, WorldDigest definition,
schedule or CR-43 metadata change. The dual requirement derives from strict
existing placement history. Older readers ignore the additive placement namespace.
The actual pre-E (`fc5688c`) and pre-CR-07 (`7c5b3921`) readers were exported,
built with the existing C compatibility test, and each opened the current
CR-07B/C fixture and started QUIESCED without changing its world/metadata.
Reproduce the documented CR-07C older-reader command with either revision;
`CR07C_OLD_READER=... go test ./internal/db -run
TestEnrollmentCR07BAndCompatibleStartup -count=1 -v` passed for both.
This proves data/startup compatibility; old binaries cannot
enforce the new login/session safeguards. A rollback must close player access,
invalidate JWT signing authority and disconnect sessions under separate operator
authorization before exposing an older binary. Data-readable is not security-
equivalent; do not declare old public login safe after rollback.

## Offline designer and future TypeScript contracts

`racegen` is an offline designer only: `-load draft.json -save new-draft.json`
and interactive save/load preserve complete `racegen.Spec`. A new draft file is
created0600 exclusively. Unfinished designs can be saved; Go validation is still
required at submission. The legacy `-db` flag fails explicitly before any database
access, and interactive enroll/password operations are retired. No bbolt import,
player allocation, PutRace writer, password hash/fallback or credentials remain.
Submission uses `gbctl` on the private administrative surface.

`api/openapi/gb-v1.yaml` is OpenAPI3.1 in JSON-form YAML1.2, without a new runtime
dependency. It defines full spec/ranges, options, homeworld/terrain choices, cost
breakdown/remaining budget, field errors, operator request, dual/legacy login,
future intent and typed placement/status outcomes. Tests compare attribute ranges
and fixed-array lengths with CR-07A. `internal/enrollmentapi` supplies pure Go
options/cost services and strict handlers; **none is mounted on the player mux**.
Future `/api/enroll/options`, `/api/enroll/cost`, mutation and status paths are
explicitly contract-only. Existing preview endpoints remain disabled/non-mutating.
No durable PENDING/ACTIVE/FAILED intent lifecycle is claimed. Future TypeScript
racegen/signup and gameplay applications remain separate; browsers are never
authoritative for cost, identity, privileges or acceptance.

## Verification and review

Final local results (2026-11-09):

| Check | Result |
| --- | --- |
| gofmt; `go build ./...`; `go vet ./...`; diff check | PASS |
| `go test -json ./...` | PASS: 3,116 tests/subtests, 2 skipped, 20 tested packages |
| Race: game/db/admin/net | PASS: 648 tests/subtests, 1 skipped, all four packages |
| A original-C fixture reproduction (`make -C test/ctool check`) | PASS: fixtures reproduce exactly |
| A/B/C/D and CR-18/CR-43 regression suites | PASS within full/race runs |
| Known-defect inventory reconciliation | PASS; existing explicit CR-18 acceptance entry unchanged |
| Lint | PASS: 1,052 matched findings, zero new/stale |
| Growth gate | PASS: five stale finding occurrences removed; no protected tooling changes |
| Offline manifests and mutations | PASS; 21 rendered resources schema-valid, none invalid/skipped |
| Workflow helper tests / policy | PASS: 180 tests / 17 policy assertions |
| Actual older-reader startup | PASS: pre-E `fc5688c` and pre-CR-07 `7c5b3921` |

Test counts include named subtests, not assertion totals. Both full-suite skips
are opt-in GB1-04 production-duration tests (`GB_LONG_TESTS`); the network one
is the race-suite skip. Ordinary GB1-04 regression coverage passed. The timeout
fault test now synchronizes on the actual started fault hook and always releases
it during cleanup; the initial race run exposed and led to fixing its test-only
synchronization defect. Loopback tests needed sandbox socket access; all
worlds/databases were disposable. No container runtime was installed or used.

The real subprocess integration covers private operator/bootstrap, two accounts,
strict negative inputs, public route absence, complete world/allocator/placement
invariance after refusals, actual dual HTTP login/JWT/WebSocket cs/survey/map/
capital, immediate actor publication, subsequent update, shutdown/restart and
same-ID replay with historical credential versions retained. Private HTTP fault
tests exercise started timeout/status/replay, rollback, confirmed post-commit
error and uncertain commit/HALTED. Focused tests cover current-session revocation,
legacy compatibility, corrupt history, preparation failure, protected files,
immutable refs, strict response validation and offline writer removal. Existing
D/C tests continue exercising post-commit panic, failed readback, concurrency,
quiesce, stale snapshots and complete rollback evidence.

Independent security/correctness review: **APPROVE WITH CONDITIONS**, no unresolved
blocking source finding. Review corrections: universe-bound authority, rejected
contradictory success envelopes, current-session revalidation and structured pure
handler failures. Reviewer independently ran full admin/provider/pure-wire suites,
race-enabled actor/auth/status tests and actual subprocess integration successfully.
Final conditions: accurate documentation, local/hosted gates, owner acknowledgment,
no merge/deploy/live operations. Hosted Docker/Trivy/Syft/kind checks remain CI work.

The owner explicitly approved removal of exactly five obsolete lint findings
from the retired writer (four unchecked Close calls and one integer conversion).
Required PR labels, **applied by the sole maintainer**: `known-defect-added` for
those five approved removals, and `gate-change-reviewed` for the isolated-fixture
correction below. No other baseline/allowance/inventory changes. Neither label
is applied by the implementation agent.

## CR-07F and public-registration prerequisites

Complete acceptance matrix and hosted CI, independent owner review, approved
operational rollout/rollback and recovery planning remain CR-07F work. Resolve
public identity/registration, account/credential lifecycle and rotation, rate
limits/session hardening (CR-20), TypeScript clients, public transport policy and
secure operator artifact distribution before any public enrollment. CR-17's
between-turn direct command writes remain unchanged; CR-07D's pending-AP/world-
agreement safeguards still prevent enrollment from overwriting them. No broad
CR-17 rewrite or automatic repair is included. Public enrollment must remain off.


## PR #28 hosted-CI fixture correction

Run `42577189156` failed mutation scenario15: `TestOpenAPIRuleMetadataDoesNotDrift`
could not open `../../../api/openapi/gb-v1.yaml` in the isolated repository.
`golden-mutations.sh` previously copied only `go/` and `test/`.

The only executable change adds the exact `./api/openapi/gb-v1.yaml` artifact to
`fresh()`'s existing tar input list, preserving its repository-relative path.
Its header now describes that dependency. All15 scenario bodies, assertion
helpers, expected outcomes and OpenAPI drift validation remain unchanged.
No application code, workflow, artifact, baseline or allowance changes accompany
this correction. This is protected gate tooling: the maintainer must apply
`gate-change-reviewed`; the existing `known-defect-added` condition also remains.

Independent review: **APPROVE**. Byte comparison confirms unchanged scenarios
and assertions. The copied artifact is byte-identical; normal drift validation
passes, while altered IQ metadata and a missing artifact still fail.

Local verification: all15 mutation scenarios produced their expected outcomes;
helper self-tests passed100 iterations per case. Full Go tests/inventory and
`go test -count=1 -race ./...`, build, vet, formatting, C fixture reproduction,
lint (1,052 matched / zero new or stale), gate self-tests, govulncheck (zero
called vulnerabilities), offline manifests, actionlint, workflow ShellCheck,
180 workflow helper tests,17 policy checks and diff checks passed. The growth
gate passed with locally simulated acknowledgment labels and correctly refused
without `gate-change-reviewed`; no GitHub label was applied. Worktree-local
TMPDIR avoided the host's unrelated invalid `/tmp` VCS marker; no VCS or test
validation was disabled. Hosted CI must rerun on the pushed correction.
