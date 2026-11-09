# CR-07D: enrollment through the authoritative actor

Status: implemented for review; no merge, deployment or live-universe operation.
Baseline: `6be0e0904923c62ccfb206a1adc554afdd0164a3` (PR #26).
The owner authorized this narrow integration before the broader Phase 3c work.

## Internal contract and trust boundary

`State.EnrollTrusted(ctx, EnrollmentRequest)` uses the existing typed actor queue
and buffered reply lifecycle. The request contains a server-assigned request ID,
a trusted caller-context reference, target game ID, `racegen.Spec` and requested
God/Guest flags. It contains no account assertion, point total, plaintext password
or caller-calculated fingerprint. The value request owns its spec arrays.

The operation is enabled only by explicitly injecting `Options.TrustedEnrollment`
at construction. The policy is copied and requires a stable game ID plus two
trusted callbacks. `Authorize` resolves the caller context into an authenticated,
game-authorized account, immutable race/governor credential version references,
and independent privilege permissions. `Prepare` resolves those references into
distinct bcrypt representations. Both run on the actor, must be bounded, and
must not call the actor recursively. Error/panic text is never logged or returned.

**This is a trusted in-process capability, not production authentication.** No
production caller, environment setting, admin route, CLI or network interface
configures it. Tests simulate an authenticated operator/account directory. A
browser-supplied context/account/ref is not authority; CR-07E must establish the
real boundary before connecting any transport. Game IDs and credential-reference
bindings must remain stable across retries/restarts. References must never be
rebound to different secrets or derived as fast hashes of plaintext passwords.

Every accepted design passes CR-07A validation and its authoritative budget.
Names separately require valid UTF-8, 1..64 bytes, no controls or surrounding
whitespace; duplicate names use case-insensitive comparison. Existing stored
names are not migrated. Requested privileges require explicit authorization;
God and Guest cannot both be requested. The actor never infers God from player1.

## Admission, identity and retries

New operations and retries require SERVING and not draining, both before queueing
and at actor admission. TURN_IN_PROGRESS, QUIESCED, HALTED and all other modes
refuse the operation. A queued cancellation never runs; a started operation can
commit after its caller times out (`ErrStillRunning`). Final enrollment metrics
are recorded on the actor, not inferred from the caller's timeout.

The actor computes SHA-256 over a versioned canonical struct containing request,
resolved account/game, full spec, privilege choices and immutable credential
references. It excludes plaintext secrets, bcrypt bytes/salts and search order.
A read-only storage admission hint checks metadata/history and CR-07C's uncertainty
guard. `GetPlacement` then recognizes the request **before** capacity, account,
name, credential preparation or placement search. A matching recorded hash returns
the original placement; changed payload, caller or reference versions conflict.
One account cannot obtain a second placement by changing ID or species name.

Replay verifies CURRENT stored race/star/planet/government state against actor
state; it does not compare the world with the original proposal hash. Later
turns, renamed/dissolved races, destroyed ships or changed capitals need not
recreate an obsolete snapshot. Unverified disagreement fails closed. A recorded
placement is historical identity/location, not a claim the race is still active
or its original government ship remains alive.

Player numbering is highest stored/historical number plus one, cap63. Dissolved
slots and gaps are retained. Storage remains final authority inside the commit.
Original `source/GB_racegen.c:80-85` requires the first newly enrolled race to be
God; this is enforced only with explicitly authorized God creation. Existing
legacy universes are not retroactively changed. There is no bootstrap operator
interface in this PR, and the production policy is absent.

## Placement, randomness and installation

CR-07B receives the actual contiguous star prefix from StarData.NumStars, not
the store loader's fixed256 slice with trailing nils. Unexpected stars beyond
that count are refused. Planets retain original C index order.

Enrollment-only Fisher-Yates permutations use `crypto/rand.Int` with rejection
sampling, producing unbiased full star/sector permutations without replacement.
No global math/rand, turn RNG or universe-generator code is used. Tests inject
full deterministic orders. No enrollment seed is persisted or replayed: a truly
uncommitted request may choose a different candidate on retry; a committed retry
uses its metadata and never searches again.

The actor obtains CR-07C's non-reserving ship ticket and reads current ships
(the existing CR-11 authority between turns). It builds a pure proposal, captures
complete star/planet/chain preimages and calls `PutEnrollment` once. There is no
immediate allocator call, reservation transaction, new commit kind or CommitWorld
for enrollment. Even a storage-eligible number is refused if it still exists in
the actor ship cache (including dead/nil keys) or any directory references it;
reuse must not retarget another player's scope. No cache/directory is repaired. CR-07A costs and CR-07B gameplay formulas are unchanged.

After a confirmed commit, independent stored copies are read back and compared
with all proposed records, ship associations, old chains and complete expected
race/star/planet/ship collections and global StarData. Replacement collections
and governor0's directory are prepared before installation. The actor installs
all objects, constructs the prompt, then calls publishWorld. Counts/gauges update
only after consistency is established. Borrowed preimages/proposals cannot alias
the installed entities. A verification/install failure halts before a stale turn
or shutdown flush can overwrite the persistent placement.

## Transaction outcomes and reconciliation

- NOT_COMMITTED: no installation/publication. Ordinary validation, authorization,
  placement and known-aborted transaction failures preserve actor state. A clean
  rollback permits a future independently validated retry. Unexpected staleness,
  transaction conflicts, corruption, fence violations or changed state after
  an aborted write halt for reconciliation.
- COMMITTED: verify and install the persisted placement. A post-commit error or
  panic from CR-07C is reported as committed_error, never rollback. If verification
  fails, halt with an explicit committed reconciliation outcome and skip shutdown
  flushing. No unverified success is returned.
- UNCERTAIN: never infer durability from a visible record. Halt, install nothing,
  publish nothing and refuse further mutations. HALTED shutdown skips flushing.
  Reusing the same tainted Store in a new State starts HALTED before any schedule
  restoration/write or timer. No guard is cleared or handle reopened by this API.

Reconciliation remains operator-controlled and is not implemented here. Preserve
placement/free-list/world evidence, inspect the explicit outcome and account
intent, and establish a consistent durable world before allowing a fresh actor
to serve. A timeout, changed request ID, fresh database handle or mere read-back
visibility is not a recovery procedure. No new durable halt marker/schema is
introduced; existing restart automation must not be treated as operator approval
for recovery from uncertainty. Real fsync/disk-full faults remain library-backed
and simulated at CR-07C's established hooks.

## CR-17, CR-18 and CR-43

Enrollment does not rewrite command persistence. Preflight rejects non-AP
race/star/planet/StarData disagreement rather than erasing uninstalled data.
There is one deliberate temporary admission result: legacy command AP deductions
can exist only in actor Star.AP/StarData.AP until the next normal turn. If ALL
other checked fields agree, return ENROLLMENT_PENDING_TURN / NOT_COMMITTED and
stay SERVING. This also temporarily refuses replay rather than claiming an
unverified result. NOT_COMMITTED describes this admission attempt, not rollback
or absence of an earlier recorded placement. No AP is merged or flushed by enrollment. A real capital
command, safe rejection, normal turn flush and successful retry are tested.

CR-17's other direct-store planet inventory changes can still leave stale actor
copies. Those discrepancies halt enrollment; their general command/turn recovery
remains outside this PR. New colony ownership, government references and directories
are installed in actor collections, so following commands, ship reload, turns
and orderly shutdown preserve the placement. Tests cover normal and God races.

CR-18 commit schema/kinds/counters/digest and turn processing are unchanged.
CR-43 quiesce/resume and durable maintenance behavior are unchanged; retries are
refused while quiesced, including after restart, and work after resume. Shutdown
messages now direct operators to the halt reason and persistent evidence rather
than implying a CR-18 record alone describes a between-turn enrollment.
Database format impact: **NONE beyond existing CR-07C placement records**.
The new storage helpers are read-only admission/uncertainty queries.

## Credentials, authentication and preview retirement

Preparation errors propagate as sanitized CREDENTIAL_PREPARATION_FAILED; no
fallback stores plaintext. Hashes require complete bcrypt shape, canonical
base64 tail bits and at least bcrypt.DefaultCost; literal race/governor hash reuse
is rejected. The provider remains responsible for safe credential acquisition,
secret management and immutable reference/version policy. Governor0 is active;
Governors stays CR-07B's C-derived0, not an accidental governor1 enablement.

The existing Authenticate API accepts ONE password and requires it to match the
race hash AND a populated governor hash. That contract is preserved. Focused
security corrections reject inactive governors, actual array bounds violations,
and empty-password race fallback for any governor above0. Active governor0
records with an empty governor hash retain the existing legacy fallback.

Distinct new credentials therefore **cannot complete login through the old
single-password HTTP/WebSocket contract**. Tests prove neither credential alone
is accepted as both, and exercise cs/survey/map/capital via the real dispatcher
at the trusted in-process boundary. These are mechanical visibility tests, not
an invented login flow or authenticated onboarding acceptance. CR-07E must define
and test the distinct-credential transport or authorized identity-token contract;
this unit does not claim complete authenticated end-to-end signup.

Legacy State.Enroll and both preview HTTP routes now always refuse enrollment,
even with GB_ENROLL_PREVIEW=true. The obsolete internal/enroll race builder and
its plaintext fallback are removed; only compatibility DTOs remain. Preview
acceptance tests are replaced with hard-disabled/no-engine-call assertions.
The old config key is accepted but ignored. No new route replaces the old writer.
The separate offline cmd/racegen database writer and its historical plaintext
fallback are unchanged and remain CR-07E retirement/security work; this PR does
not claim all repository password fallbacks are fixed.

## Validation, review and CR-07E prerequisites

Local evidence covers complete actor/storage enrollment, restart/turn/replay,
actual timer scheduling, concurrency/cancellation/started timeouts, all modes,
God/Guest permissions, capacity/gaps/history, malformed credentials, strict
metadata, live/dead allocation conflicts, stale preimages/chains/free tickets,
all write-fault stages, committed/uncertain/read-back/install failures, no stale
shutdown flush, independent copies, normal-race commands and bounded metrics.
Focused coverage is 32 actor tests / 114 leaf cases plus two new storage
admission tests / four leaf cases; existing suites remain intact. The
implementation PR records final gate results.

Local PASS: formatting/build/vet, full Go tests and inventory, db/game race
tests (including CR-07A/B/C, CR-18/43), original C fixture reproduction and
golden tests, lint (1,057 matched; zero new/stale), growth, offline manifests
(21 valid resources), workflow helpers (180 tests), policy (17 checks) and
diff check. Final full race run: game81.917s, db10.878s.
Container builds, image scans and kind run only in GitHub-hosted Actions. No
protected tooling, golden allowance, defect inventory or lint baseline changes
are made; no acknowledgment labels are required.

Independent review found and corrected stale-race/collection overwrite risks,
fixed-size star-loader adaptation, pending command AP admission and tainted-handle
startup. Final independent source/test/documentation review: APPROVE for focused PR;
no blocking findings remain. Hosted CI and owner review remain merge conditions.

CR-07E must supply real operator/account/game authentication, immutable credential
provisioning, explicit God bootstrap, the distinct-credential login contract and
transport acceptance tests, request/rate/body limits and durable reconciliation
workflow. Retire the legacy offline writer safely. Address/document CR-17 state
synchronization before exposing signup. The separate public TypeScript racegen
application and TypeScript gameplay client remain later work. No deployment,
Kubernetes, live universe, external signup or publishing operation occurred.
