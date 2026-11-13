# CR-17C #41B: hostile ship-unload Phase 0

Status: **BLOCKED before production implementation: explicit ship retreat-mass disposition required.**
Issues #41, #51 and parent #38 remain open. This is not implementation approval or full acceptance.

Baseline/master: `7a17a1bc7edf75091386f30944a529a0ccf48d8d` (PR #78).
Branch `feature/cr17c-ship-ground-assault`; isolated worktree `/tmp/GB-cr17c-ship-ground-assault`.
Other worktrees, the previous ground-assault audit and shared Claude checkout are preserved.
No production code, existing tests, gates, baselines, schema or commit kinds changed.

## Mandatory owner decision

C `source/load.c:1071` subtracts departing population mass; its retreat branch
at line1130 subtracts returning survivor mass AGAIN. Go's currently unreachable
`unloadOntoAlienSector` instead credits returning mass (`commands_load.go:992`).
Neither is a carrier aggregate-mass branch: containing-carrier/reciprocal-peer
transfers follow the ship-recipient path and never enter this planet helper.
The earlier carrier exemption approval therefore does not resolve this decision.
The PR #78 Phase0 audit explicitly deferred ship-mass approval, and the present
request expressly excludes ship-specific mass deviations from its general policy.

Requested narrow disposition: retain the dormant Go mass-conserving survivor
refund when enabling the new operation. With original mass M, departing Q,
returning survivors S, and authoritative caller-race unit mass m:

- Repulse: `M_after = M - (Q-S)*m`.
- Victory, mutual destruction or total attacker loss: `M_after = M - Q*m`.
- Absorbed bodies remain in the sector and do not add ship mass.
- Check finite/nonnegative mass and representable complement; reject unsafe
  preimages instead of clamping or repairing them. No parent/carrier adjustment
  is introduced into the planet-root-only slice.

Do not enable either a literal negative-mass C implementation or a newly
reachable corrected rule without the owner's explicit disposition.

## Exact operation and participant inventory

| Step | Witness/read set | Proposed mutations | Existing C/legacy Go persistence and effects | Conditional atomic disposition |
| --- | --- | --- | --- | --- |
| Selection/authority | Explicit selected number/map identity; caller race and active governor; ship owner/governor/alive/active; directory | None | C uses authorized(selected ship); legacy Go lacks some accepted checks | Reuse #41A exact selector/context capture; never borrow movement-only star-governor/slavery policy |
| Landing | Docked, WhatDest/WhatOrbits PLAN, matching source/destination star/planet; landing coordinates; complete live unique root/parent/peer identities | None | Old bad-partner branch can independently undock/save | Require consistent actual planet root membership; never invoke repair or ship-recipient assault |
| Mechanical/transporter boundary | Complete root ships and relevant third-party/alliance races; selected transporter | None | C hostile helper has NO AFV screening; Go/C landed predicates differ | Refuse deferred mechanical contexts conservatively before draws; preserve PR78 peer/AFV regression; active targeted transporter refused |
| Quantity/default | Ship Popn/Troops; effective MaxCrew including factory ability; landing sector; command sign | None | C validates signed interval before expanding default | Independently recompute exact c/m bounds; no truncation/clamping |
| Departure | Selected complete ship; caller Race.Mass | Selected commodity complement and Mass | C/Go mutate ship before combat, output unload/complement early | Private proposal; include complete selected ship write |
| Combat | Both complete races, sector terrain/likes, ship Armor/Damage, bounded random transcript | Party/defender casualties | ground_attack uses fixed original strengths and casualty scale | Preserve ship-specific multipliers, no fresh storage draws |
| Outcome/absorption | Original and final force counts, body pool, capacity | Sector population/troops/Owner; ship returning complement/mass | C can assign owner/resurrect on mutual; Go same general old defects | Apply approved PR78 survivor, neutral mutual, empty occupation and bounded nonresurrection policy; mass still needs approval |
| Accounting | Complete embedded sectors and per-player/global totals | Planet Popn/Troops, both owners Info totals, actual ownership counts and MobPoints | Helper changes capture counts but omits coherent population aggregates | Recompute checked final permitted deltas; reject inconsistent preimages, preserve unrelated records |
| Race results | Both full races and original ownership | Morale, Translate, C Points | C putrace twice independently; legacy Go helper differs in Points | Include both races in same transaction; preserve ship-specific extinction learning |
| AP | Root star/universe AP and directory identity | None | C dispatcher passes0; helper returns before ordinary tail; legacy Go wrongly checks1 | Zero AP, immutable full context witness; no new charge or movement cost |
| Reporting | Recipient owner/star governor; existing news stream/inbox keys and sequences | One combat-news entry and required defender telegram, sequence | Old helper early messages/warn, volatile groundAssaults counter; separate writes | Reuse existing transaction-local news/inbox allocation; no aggregate increment or duplicate turn report |
| Commit/install/delivery | Full participant preimages, fence, maintenance and uncertainty guard | Atomic ship+planet+bothraces+messages; stable actor installation | Old writes can split and stale stored planet copy disagrees with actor | One checked bbolt Tx; then identity-preserving complete installation and buffered output; uncertain => HALT/no stale flush |

Conditional writable set: selected ship, complete affected planet, attacker race,
defender race, existing combat-news/inbox records and inbox sequence. Star/AP,
other ships, orbital context and alliance races are immutable witnesses. No
allocator, persistent RNG, command ID, bucket, schema, commit kind or outbox is
needed on current evidence. A separate closed ship-assault contract should share
checked internals without pretending it is sector-origin movement.

## Grammar, C comparison and random preparation

C load.c validates signed bounds before default expansion. Unload negates the
signed amount. A valid positive unload attacks; omitted/explicit zero historically
selects the entire complement if within the pre-default interval. Empty complement
resolves to zero and the helper refuses without combat. Negative unload reverses
into loading; hostile load/reverse must remain excluded from the new slice.
Current #41A rejects ALL foreign-sector c/m variants before legacy mutation.
The future interface must classify these variants before draws/effects and must
not treat a failed/newly excluded operation as permission to use the old writer.
Document explicit zero against C default semantics: distinguish a default that
resolves to a valid positive complement from an actual zero-sized force. Never
invoke combat for zero people or allow signed/reverse variants through fallback.
The pre-default VN prohibition has a historical default-order edge; do not invent
a new blanket type rule or silently alter accepted noncombat semantics.

C ship attacker strength uses `(Armor+1)*(2-Damage/100)`, unlike movement terrain
and race affinity. Preserve the military factor, fixed pre-casualty scale,
integer-division order, stochastic rounding and inclusive integer draw order.
The accepted private isolated random source and bounded typed transcript can be
adapted; storage verifies the transcript without generating fresh draws. No
retry/reroll after stale state or uncertainty. Empty defense bypasses undefined
C strength division under the already approved occupation policy.

C ship helper ALREADY assigns captured ownership; do not misdescribe it as the
historical movement missing-assignment bug. General mutual/absorption/accounting
corrections are already approved, whereas the ship-mass formula above is not.
No broad same-owner, governor, terrain or balancing policy is added here.

## Fresh execution evidence

Go1.27.4, GOTOOLCHAIN=local. Evidence archive and SHA256 ledger accompany this
record. Runtime tests use disposable files and actual actor HandleCommand.

1. Private Go test overlay `TestShipAssaultPhase0Refusal`: eight civilian/military
   × positive/omitted/zero/negative unload leaves passed under `-race` (9 nodes
   including parent; package3.764s). Every case compares the entire captured actor-world snapshot
   and every logical bbolt record plus every bucket sequence before/after.
   Logs contain only refusal text, snapshot hashes and record counts, not secrets.
   Overlay adds audit tests only; it does not disable the hostile refusal guard.
2. Fresh accepted population+ground-assault race regressions:465 passed nodes,
   zero failures; rules1.093s, db6.779s, game22.622s. All24 #41A SIGKILL leaves
   and20 PR78 SIGKILL leaves executed unchanged. These are accepted operation
   regressions, NOT new hostile ship assault/crash acceptance.
3. Compiled UNMODIFIED source/load.c, move.c and rand.c with a private C wrapper.
   The wrapper calls the original assault helper, supplies deterministic raw
   random draws, records notifications/race-write calls and uses private value
   records. Its label/IO adapters are not durability evidence or full C command
   dispatcher execution. Unreached callbacks abort99. C AP0 is additionally
   established by GB_server.c:1557/1625, not inferred from the helper alone.

| Controlled C helper case | Initial mass | Final mass | Final selected complement | Result |
| --- | ---: | ---: | --- | --- |
| No-casualty retreat (Q100, S100, m2) |210|**-190**|100 civilians unchanged|Defender retains sector; literal C physically invalid |
| Partial civilian retreat (Q100, S50, m2) |700|400|150 civilians|Correct survivor refund would give600 |
| Partial military retreat (Q100, S50, m2) |700|400|150 troops|Same independent sign defect |
| Victory (Q100, all survive ashore, m2) |700|500|100 civilians remain aboard|Departure debit only |
| Mutual destruction (Q1, m2) |302|300|0 civilians|C wrongly leaves empty sector owned by attacker; approved neutral policy applies later |

All five C runs exit0. First C build failed at link time because imported audit
stubs duplicated use_fuel/use_destruct and omitted unused selection symbols.
Only private harness stubs were corrected; the failed stderr is preserved.
The successful rebuild uses original production C translation units unchanged.
No undefined empty-defender C formula was executed as a parity oracle.

## Review, validation limits and next step

The separate read-only Phase0 source reviewer completed review and recommends
STOP for the narrowly scoped mass decision. Source review is not implementation
approval. Its report and subsequent runtime-evidence revalidation are preserved
separately with hashes; no historical PR78 evidence is rewritten.

No new implementation, full-suite acceptance, new ship-assault SIGKILL cases,
negative-control acceptance, hosted CI, commit or PR is claimed. Full normal,
race/long, golden, lint/growth, security and other implementation gates must run
after authorized implementation; focused Phase0 execution is not a replacement.
No protected tooling, allowance or lint-baseline change; no acknowledgment label
is required for this audit-only diff. Issues #41/#51/#38 remain open.

After owner disposition, implement the closed ship operation and complete all
requested gameplay, failure, actual SIGKILL, causal negative-control, full-gate
and independent implementation-review requirements. Preserve this Phase0 archive
as history. No merge, deployment, images or live-system operations occurred.

## Preserved evidence identities

- [Phase0 source review](evidence/cr17c-ship-assault-review.md): completed,
  recommends the explicit mass decision; no implementation approval.
- [Independent runtime revalidation](evidence/cr17c-ship-assault-evidence-review.md):
  independently rebuilt all five C cases and reran eight actor refusal leaves
  under race (game3.792s), confirmed all source/helper comparisons.
- [Archive](evidence/cr17c-ship-assault-phase0.tar.gz) includes commands, harnesses,
  preserved failed build, raw test outputs, review histories, source hashes and
  per-member SHA256SUMS. Archive SHA-256: `b645d885bd4673046755a620482001558fc107d06933ca91e0048749e22b329d`.
- [Validation ledger](evidence/cr17c-ship-assault-validation.json) records source,
  document, archive and test evidence identities and exact execution counts.

The focused regression filter explicitly does not execute inventory validation.
No full gate result is inferred. Current origin/master was rechecked and remains
`7a17a1bc7edf75091386f30944a529a0ccf48d8d`. Worktree HEAD is unchanged; only
these audit/evidence files are pending. No commit, push or PR is created while
the owner decision is outstanding.
