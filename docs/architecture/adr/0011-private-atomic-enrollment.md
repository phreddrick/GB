# ADR-0011: Actor-owned atomic enrollment with a private operator trust boundary

Status: Proposed 2026-11-09 (records the owner-authorized decisions implemented
and merged in CR-07A–E; CR-07F acceptance and normal PR review apply).

## Context

CR-07's original Go writers created only a race, leaving no playable colony or
government ship. They also bypassed the running actor and had unsafe credential
fallbacks. The original built enrollment writer is `source/GB_racegen.c`, not
the unbuilt `enroll_valid.c` draft. ADR-0001 keeps one authoritative engine;
ADR-0009's commit record has a closed set of turn kinds, and ADR-0010 protects
maintenance intent. Public account identity and a TypeScript signup app do not
exist yet. The owner authorized A–F ahead of the broader Phase 3c sequence.

## Decision

1. **One writer and one placement transaction.** `State.EnrollTrusted` runs on
   the existing actor. CR-07A validates the full `racegen.Spec`; CR-07B constructs
   independent proposed entities; CR-07C rechecks complete preimages, vacancy,
   player/ship identities and orbital links inside one bbolt write transaction.
   Race, star, planet with embedded capital, government ship, allocator update
   and caller-bound placement metadata commit together. Only confirmed,
   verified state is installed before publication. No separate reservation,
   direct offline writer, second database handle or enrollment turn kind exists.
2. **Historical identity and conservative allocation.** Player numbers append
   above stored races and placement history, cap 63; dissolved slots are not
   reused. Names are case-insensitively unique. One placement per authorized
   account is enforced in storage. A non-reserving ship ticket binds the free
   list and is rechecked in the transaction. Existing live/dead ship keys,
   historical/dangling references, unsafe tails and overflow fail closed;
   enrollment does not reclaim records or change the global allocator.
3. **C-derived placement with explicit safety decisions.** Defined C cost,
   rounding, calibration and metamorph covariance are preserved; eight liked
   sector types have the approved 600-point extension instead of undefined C
   memory access. Home mappings follow C; stars require at least two planets,
   no inhabited races and a matching planet with RTEMP -200..100 inclusive.
   Planet/sector ownership is checked despite stale flags. Search uses isolated
   unbiased crypto/rand permutations, never the turn RNG; planets retain C index
   order. Capital terrain comes from the last common 100% compatibility. Missing
   terrain fails, never falls back to (0,0). Existing valid ship chains are
   prepended safely. Colony and ship values follow C, without C++ enhancements,
   arbitrary stockpiles or reproducing undefined translation-array writes.
4. **Retry before reconstruction.** Schema 1 placement metadata lives under
   `meta/enrollment/v1/placement/<bounded_request_id>`. Strict decoding rejects
   corruption. The actor's canonical fingerprint binds the trusted account,
   universe, complete spec, privilege choices and immutable credential refs;
   it excludes plaintext, bcrypt encodings/salts and search orders. Matching
   durable retries return the historical placement before allocation, credential
   preparation or search, even after turns, rename or government destruction.
   Changed intent/caller/version conflicts. Current actor/store disagreement
   cannot become an unverified success. No RNG seed replay is claimed.
5. **Private authority, no public registration.** The existing authenticated
   admin listener uses one operator service principal and a protected,
   universe-bound allowlist manifest. Missing policy denies enrollment. Operator
   JSON account/game/privilege claims grant no authority by themselves. First
   God creation requires explicit request, account permission and separately
   enabled bootstrap; subsequent God creation is denied. Guest authorization
   is independent; God and Guest are mutually exclusive. Historical first-God
   retries may succeed after disabling bootstrap. No general identity service
   or public signup endpoint is implied.
6. **Independent credentials and current authority.** Protected-file
   `gbctl credentials prepare` independently bcrypts distinct 12..72-byte
   race/governor passwords, with no truncation/plaintext fallback/hardcoded 0.
   Immutable salted encoding versions bind account/game/kind and must be
   retained for retries. A manifest update cannot rotate an already placed
   race's stored credentials. New races require independent dual-credential
   login, derived from trusted placement history. Safe historical legacy login
   remains. Inactive/out-of-range governors and fallback to another governor
   are refused. JWT/WebSocket admission and command authority are revalidated
   against current credentials/activation/privileges. CR-20 is not completed.
7. **Failure evidence outranks transport outcomes.** Known-aborted writes
   install/publish nothing. Confirmed commits stand despite later reporting
   errors; failed verification halts before a stale turn/shutdown flush.
   UNCERTAIN halts, installs nothing and retains the Store guard. A visible
   record, timeout, HTTP200 alone, fresh request ID or reopened handle is not
   reconciliation. There is no automatic repair/resume. Preserve complete
   world, placement and allocator evidence for a separately authorized operator
   investigation. Mutation/retry requires SERVING and not draining; private
   read-only status has its explicitly documented maintenance policy.
8. **Compatibility and remaining command debt.** Placement metadata is additive;
   CR-18 schema/kinds/digest and CR-43 records are unchanged. Actual earlier
   readers accept the store, but lack newer login/session protections: data
   compatibility does not establish security-safe rollback. CR-17 AP-only
   divergence temporarily refuses enrollment pending a normal turn; other
   stale disagreement fails closed. No broad command-persistence rewrite occurs.

## Alternatives considered

- Direct offline writes: rejected because the actor can later overwrite them.
- A new CR-18 enrollment kind or destructive migration: rejected because older
  strict readers would reject the store; the dedicated transaction is sufficient.
- Public browser claims/static admin bearer as player identity: rejected. The
  bearer authorizes the configured private operator only.
- Treat visible read-back as durable after an I/O error, or automatic reopen:
  rejected because it destroys the distinction between uncertainty and success.
- Reproduce unsafe C fallback/undefined memory operations: rejected explicitly
  by owner-approved parity and safety decisions.

## Consequences

Private operator-created races have complete playable starting empires, immediate
actor visibility and durable retries. Safe refusals can reduce enrollment
availability when legacy command drift or a historical free-list tail exists;
they do not authorize silent repair. The offline designer cannot open a game
database. Future Go-owned OpenAPI contracts remain unmounted on the player mux.
Public registration, identity lifecycle/recovery, abuse controls, query-token
hardening, TypeScript signup and gameplay clients and live rollout remain later
work. Any rollback needs closed player access, disconnected sessions and new JWT
signing authority under separate authorization before exposing an older binary.

## Validation

[CR-07F acceptance matrix and execution evidence](../../testing/cr07f-enrollment-acceptance.md)
maps requirements to fresh A–E suites, independent whole-model C expectations,
real gbctl/HTTP/JWT/WebSocket journeys, both-race turns/restarts, synchronized
concurrency, real SIGKILL transaction windows, uncertainty/failure tests and
actual earlier readers. The crash barrier is compiled only with the separate
`gb_enrollment_acceptance` test tag; the ordinary binary ignores its environment
key. No debug route or repair control is added. Physical disk/fsync failure
injection and live operational acceptance are not claimed.

## References

- CR-07 audit sections 2.2–2.3 and independently reviewed follow-up.
- `docs/testing/cr07a-racegen-rules.md` through `cr07e-enrollment-interfaces.md`.
- ADR-0001, ADR-0009, ADR-0010; migration plan Phase 3c (enrollment slice only).
- `docs/research/external-racegen-webapp-requirements.md`.
