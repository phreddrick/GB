# CR-07C: atomic enrollment storage

Status: implemented for review; no merge, deployment or live enrollment.
Baseline: `3d3898a8b5e57342a0a4cffb4d91058d79118e6c` (PR #25).
Owner authorization permits this storage unit ahead of Phase 3c. CR-07A costs
and CR-07B gameplay semantics are unchanged.

## Contract and transaction

`db.Enrollment` accepts trusted CR-07B race, planet, star and government-ship
proposals using model types; db does not import game. It also requires full
expected star/planet preimages, every existing planet-chain ship in link order,
a read-only allocation ticket, capital coordinates, request/account identities,
a canonical request fingerprint and an explicit creation time. Callers must
exclude concurrent input mutation. No model is modified by the operation.

`EnrollmentShipCandidate` reads the current free-list tail or highest ship key
plus one. It reserves nothing. Supply this ticket's number to `ProposePlacement`,
then pass both outputs to `PutEnrollment`. The ticket binds the free-list
sequence, keys and values with SHA-256. Availability, references and the complete
ticket are rechecked **inside the same write transaction**; a stale candidate
requires a new proposal. There is no separately committed allocation.

The one bbolt `Update` writes precisely the new race, updated planet (including
capital), updated star, new government ship, optional free-list pop and placement
metadata. The inserted ship record makes a new high number unavailable. A
free-list deletion and all world writes roll back together. No `CommitWorld`,
new commit kind, schedule/counter/RNG write or actor enrollment call is used.
The CR-18 fence is checked at admission and again after acquiring the write
transaction; refusal uses existing accounting and `ErrWriteFenced`.

Preconditions compare complete decoded star/planet models and existing-chain
ship models. They also validate identities, capital availability, the exact
allowed CR-07B planet/star changes, ship associations and chain topology. A newly
appeared unlinked live ship at the target planet is stale even when its head is
unchanged. Other existing ship records are preserved. Race allocation is
append-only above the highest stored race or historical placement, capped at
63; gaps are not reused. Dangling player ownership anywhere in stored ships,
stars or sectors also conflicts. God privileges and final identity allocation
remain future actor responsibilities. Gameplay-derived values such as MaxPopn
are trusted CR-07B outputs, not independently recalculated by storage.

## Allocation and dead records

Enrollment alone validates every free-list key/value length, nonzero sequence
and number, sequence bounds and duplicate numbers. It rejects maximum-number
overflow, every existing live **or dead** record at the selected ID, and dangling
references from ship links/cargo/destinations/protection/legacy target/special
mirror/transporter fields, star/planet/universe roots, race government ships and
prior placement metadata. It never skips an unsafe tail or deletes a dead record
to permit reuse. Global `AllocShipNum`, `DeleteShip`, turn allocation and CR-18
free-pop behavior are unchanged.

This conservative policy can block later enrollment when a destroyed original
government ship becomes the free-list tail: its historical placement still
references that ID. Reclamation requires separate design/review; CR-07C does not
repair chains, discard history or weaken CR-07B's refusal of existing dead IDs.

## Additive metadata and idempotency

Database format impact: **additive meta records only**, no migration or changes
to existing model layouts, CR-18 schema/kinds or WorldDigest. Keys are
`enrollment/v1/placement/<request_id>`; IDs contain only ASCII letters, digits,
underscore or hyphen, with request length 1..64 and caller length 1..128.

Schema 1 requires request ID, authoritative caller/account ID, canonical request
SHA-256, store-derived proposal SHA-256, player, home star/planet, capital x/y,
map width/height, government ship, UTC timestamp and record SHA-256. The proposal
digest binds all four complete prepared models plus capital coordinates; the
record checksum binds its internal identities and coordinates. Checksums detect
inconsistency, not malicious database tampering. Metadata contains no plaintext
secrets or complete model values.

The caller must authenticate the account and compute the canonical fingerprint
from server-owned non-secret design data and opaque credential references or
versions. Do not use a browser hash/caller ID as authority or publish fast hashes
of plaintext passwords. Prepared credential fields must already satisfy the
future workflow's credential policy; storage does not hash, inspect or log them.

Lookup precedes name, consumed-slot and stale-world checks. An identical request,
caller, canonical fingerprint and prepared proposal returns the original record
without another placement. Normal replay opens no write transaction. Changed
payload/proposal or another caller conflicts. A different ID/name cannot bypass
one-placement-per-account policy. Historical player/homeworld assignments also
cannot be reused. No administrative override is provided.

`GetPlacement(callerID, requestID)` is read-only: nil/nil means absent; corrupt
records, I/O failures and caller mismatch are distinct typed errors. Its caller
ID must already be authenticated and game-authorized. Required fields, exact
field names, unknown/duplicate fields, nulls, trailing JSON, version, IDs,
coordinates, timestamps, checksum/key identity and duplicate account/player/
ship/homeworld assignments are validated. Corruption never means absence.
There is no lookup endpoint. A lookup error's NOT_COMMITTED describes no write
by that lookup, not proof that a previous enrollment failed.

## Failure outcomes

`EnrollmentError` exposes a stable code and separate outcome, without raw model
or hook error text:

- `NOT_COMMITTED`: no transaction attempted, rejected callback or callback
  panic. bbolt rolls back the callback; returned placement is nil.
- `COMMITTED`: bbolt returned success and later notification handling failed or
  panicked. The placement stands; no rollback is claimed.
- `UNCERTAIN`: commit attempt failed/panicked, or a simulated commit error was
  reported despite visibility. Read-back can provide evidence but never proves
  fsync durability. Read failure/panic preserves uncertainty.

After uncertainty, **all** enrollment writes on that Store are refused. No
automatic retry, including a changed ID/account, is permitted. The future actor
must halt and reconcile. Reopening a handle is not a retry/reconciliation policy.
A visible `GetPlacement` result is not a durability certificate for an uncertain
operation. This in-process taint does not alter the existing global write fence.

Fault injection uses existing `SetMetaFault` with enrollment stages documented
on `PutEnrollment`. Rollback tests verify all bucket values **and sequences**,
including placement metadata/free-list state omitted by simple record counts.
Tests assert each intended hook was actually reached. Real disk-full/fsync/page
write faults are not injected; bbolt atomicity and default synchronous commits
remain the library boundary, as in CR-18.

## Validation and compatibility

Local results:

- 27 enrollment tests, 154 leaf cases, including actual CR-07B output, complete
  model comparison after reopen, existing orbit chains, concurrency, metadata,
  all fault stages, allocator corruption/dead records and uncertainty guards.
- Go formatting, build, vet, full unit/e2e suite and known-defect reconciliation;
  race-enabled db/game tests; existing golden tests and original C fixture
  reproduction. No original C, CR-07A/B rule or golden allowance changes.
- Lint: all 1,057 existing findings match; zero new/stale findings. Growth gate:
  no protected tooling or allowance changes; no acknowledgment labels required.
- Offline manifest structural/mutation/safety checks and 21 schema-valid
  resources; workflow helper tests (180) and policy checks (17); diff check.
- Container builds, image scans and kind integration remain GitHub-hosted CI.

The external-package integration test avoids a db→game dependency and compares
all four complete stored entities against `ProposePlacement`, plus existing
chain ships. It checks unchanged raw commit, schedule, universe and maintenance
bytes and starts the current reader in an isolated QUIESCED fixture. Shutdown
flush is deliberately aborted with the existing test hook.

Actual older readers built from baseline `3d3898a` and pre-CR-07 master
`7c5b3921` also opened that fixture,
validated CR-18/CR-43 and started QUIESCED without changing world/metadata. The
placement key is ignored because old startup reads only named metadata keys.
Existing stores without placement records still pass the full legacy suites.
WorldDigest does not cover arbitrary placement metadata: tests inspect it and
the free list separately.

Reproduce the old-reader proof from the CR-07C worktree (all paths disposable):

```sh
legacy_tree="$(mktemp -d /tmp/gb-cr07c-reader.XXXXXX)"
git archive 3d3898a8b5e57342a0a4cffb4d91058d79118e6c | tar -x -C "$legacy_tree"
cp go/internal/db/testdata/cr07c_legacy_reader_test.go "$legacy_tree/go/internal/db/"
go -C "$legacy_tree/go" test -c -o "$legacy_tree/reader" ./internal/db
CR07C_OLD_READER="$legacy_tree/reader" go -C go test -race ./internal/db \
  -run TestEnrollmentCR07BAndCompatibleStartup -count=1 -v
```

Repeat with `7c5b3921e5c5222b7a581d7175a7c3160ea5bbd2` in the archive
command to verify the pre-CR-07 reader; both exported readers passed locally.

The copied fixture is a test, not a new operational writer/CLI. Without the
optional reader binary the integration test always exercises current startup.
The separate allocator helper file isolates enrollment-only safety checks;
`store.go` only embeds enrollment synchronization/uncertainty state. `keys.go`
and shared allocator helpers need no changes; namespace/schema stay together.

Independent review: APPROVE for focused PR after fixing rollback panic results,
unlinked-orbit staleness, read-back panic recovery and uncertainty retry bypass.
Independent race-enabled enrollment tests with the baseline reader passed;
final hardening review found no blocking findings. Hosted CI remains a merge
condition; owner reviews and merges separately.

## CR-07D handoff and remaining blockers

Only the authoritative actor may call this API between turns, while serialized
against world changes and after mode/privilege checks. It must authenticate and
bind the account/game, validate CR-07A, recognize durable retries **before**
rebuilding placement (compare server canonical RequestHash), choose append-only
player identity, obtain the non-reserving ship ticket, capture complete expected
preimages/chain, run CR-07B with isolated enrollment randomness, then persist.
Install independent world proposals and publish state only after confirmed
commit. Halt/reconcile uncertainty; never infer failure from timeout or reuse a
number after an uncertain write. Replays after restart use lookup first; a new
bcrypt salt or changed current world cannot reproduce the original proposal hash.

No call may target a database owned by a running server from another process.
CR-17's per-command stale-write risk remains; installation must update all actor
references. No RNG persistence/selection policy or account recovery override is
chosen here. God-first/privilege policy, separate governor-password enforcement,
identity binding and exposure remain later decisions. The plaintext-password
fallback and inactive-governor authentication defects remain blockers before
public enrollment. This unit fixes neither. Public TypeScript racegen/signup
and gameplay clients remain separate later applications. No routes, active
enrollment, live database access, deployment or Kubernetes operations were added.
