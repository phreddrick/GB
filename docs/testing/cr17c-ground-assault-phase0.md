> Historical Phase 0 record. The owner subsequently approved the bounded
> contract; see [implementation continuation](cr17c-ground-assault.md). The
> BLOCKED status and no-implementation statements below describe that earlier
> execution, not the resumed work. Original evidence remains preserved.

# CR-17C #41B / CR-19: Phase 0 audit and approval boundary

Baseline `b3ac84d404cc180400336f91dadb828b52c5491a`; issues #41, #51 and #38.
Branch `feature/cr17c-ground-assault`, worktree `/tmp/GB-cr17c-ground-assault`.
**BLOCKED before production implementation.** The audit/reproductions below are
completed; no combat fix, transaction API, schema or retry policy is implemented.
All runtime experiments use disposable worlds and Go 1.27.4/GOTOOLCHAIN=local.
Other worktrees, the shared audit checkout and live systems are untouched.

## Operation and side-effect inventory

This table was established before any production implementation. P means the
complete planet including embedded sectors and every affected per-player Info;
A/D are attacker/defender races; S is the source star; U is universe star data.
Additional mechanical-defender owners are separate race participants, not
implicitly the sector's owner. All references below are current-baseline lines.

| Operation | Read witnesses and identities | Actor/proposed mutations | Existing persistence | Random draws and external effects |
| --- | --- | --- | --- | --- |
| `move/deploy` admission (commands_move.go:336–391; C move.c:175–222) | Authenticated player/governor, directory PLAN scope, S identity/governor, P identity/map bounds/slavery, origin Owner | None until accepted movement | Some validation exits call unchecked PutPlanet even without meaningful change | Scope/authority/coordinate errors; no RNG |
| Direction/quantity loop (Go:393–476; C:205–270) | Entire direction string, every visited sector, map wrapping/adjacency, source commodity, requested signed quantity | Loop coordinates, local party; later steps use results of earlier ones | Earlier steps can already have written races, ships and telegrams; P usually saved at return/end | Early `population/troops moved` text precedes combat/AP/persistence; C negative means leave N behind; Go's negative branch is unreachable |
| Mechanical screening (Go:156–214; C:692–737) | Complete P ship root/list; each ship Number, NextShip, alive/location/landing/AFV/guns/ammo; both races' mutual alliances, tech/likes/morale; sector terrain; owning governor | Party casualties; participating AFV Destruct/Mass; counterattack Damage/Alive/WhoKilled/Notified, crew/troops/guns/calibers; killed-owner GovShip and involved race morale/points | C putship per defender and putrace in kill_ship; Go updates actor ships and races without checked coordinated persistence | Per-volley roundRand/intRand, per-person/gun collateral success rolls, damage/kill messages, actor replies and defender warn. C kill_ship can recursively kill carried children or undock peers, extending the record set (GB_server.c:1819–1851); Go killShip omits that recursion |
| Mechanical abort/AP refusal (Go:472–489; C:263–292) | Surviving party and S.AP | Mechanical effects may already exist before insufficient-AP result; all-killed party aborts before ground combat | Independent mechanical/race effects are not rolled back; unchecked P save | C and Go debit origin only after mechanical screening; mechanical deaths can remain at origin. No-party abort has no movement AP debit |
| Assault admission/AP (Go:486–515; C:279–318) | P origin/destination; A/D IDs and all combat attributes; S.AP and defender governor | Pending assault count; +5 bounded translation both ways; departing party removed from origin | No atomic operation; counter only in State | AP = `MOVE_FACTOR*(floor(ln(1+surviving civ)) + hostile)+1`, military uses log10. Planet scope uses star AP, not U.AP. Counter increments before human combat |
| Human ground combat (Go:528–541; C:322–332,833–867) | A/D Fighters, Morale, Likes; origin/destination defense multipliers; counts and civilian/military mode | Party survivors; destination Popn/Troops casualties | Local/actor mutations before durable boundary | Three stochastic roundings and up to three integer casualty draws; strengths fixed before casualties; military division by10 precedes multiplication in two casualty bounds |
| Movement victory/absorption (C:333–351; Go:543–580) | Defender eliminated, original defender bodies, attacker Absorb | C assigns destination Owner then incoming residents/absorbed civilians; A/D morale and C winner Points; mobilization transfer | Go **never assigns destination Owner first**, so its victory branch is unreachable for hostile sectors; PutRace calls are separate | Optional absorption integer draw; capture/absorption text and defender notice; Sector.Race is not reassigned by either C path |
| Repulse (Go:581–601; C:352–369) | Defender survivors and Absorb; attacker casualty count | Attacker survivors return to origin; defender may absorb killed attackers; morale/points favor defender | Independent PutRace(alien), PutRace(attacker), later PutPlanet | Optional absorption draw; repulse text and warnings |
| Learning/reporting (Go:603–628; C:371–424) | Original owners/governors, names/terrain, survivors, original casualty counts | Additional bounded translation changes; pending assault counter remains in memory | warn may independently append an offline telegram; live notifications have no durable record | Attacker casualty text, defender battle summary, governor0 fallback; no immediate standard human-assault news entry |
| Sector cleanup/next step (Go:631–662; C:426–461) | Final populations and ownership | Empty origin/destination Owner0; ownership/mobilization accounting required; AP debit and new loop position | Go P save and both Race saves are separate; AP debit is only actor memory until later turn/shutdown. C may exit `done` without final putplanet, although putsector/putrace/AP have executed | `Finished` or next-step text; later invalid step can follow already visible earlier effects |
| Ship selection/docking (C load.c:54–280; Go commands_load.go:60–285) | One selected ship owner/governor/alive/active, scope/coordinates, landing/orbit roots and membership; race Mass, capacity; planet/sector/defender | Legacy may undock a broken partner independently; new #41A intercept prevents entry for hostile c/m | Legacy uses separate GetPlanet copy; final saves independent | Prefix location/docking text; amount sign reversal/default and VN restrictions precede assault; legacy Go checks AP1, but assault returns before debit; authoritative C load/unload cost is0 |
| Current hostile ship command (#41A) | Same typed population authority/context; target Owner | **None** | No writes; `PlanPopulation` returns ErrPopulationDeferred before mutation | Actual `unload #77 c 1000` returns deferred refusal. Do not describe the unsafe helper as currently enabled |
| Ship assault helper (C:1040–1182; Go:851–1057) | Ship and sector, A/D Mass/Fighters/Morale/Absorb/Likes, armor/damage, star governor | Remove party and its race mass from ship; sector combat; capture or return survivors; both races and counters | Legacy helper writes both races, warns defender, enclosing doLoad saves ship and independent P copy. Actor P remains stale | Same human-combat draws, optional absorption, unload/complement/casualty/capture/repulse messages. This helper does **not** run mechanical-defender screening |
| Ship capture/repulse accounting | Original defender identity and mobilization; final residents | C and Go explicitly assign captured Owner. Both adjust ownership counts/mobilization on capture but omit coherent planet/per-owner population deltas. C repulse subtracts returning mass again; Go adds it correctly | Multiple independent writes; successful text before all records | C/Go empty/no-survivor and absorption edge behavior requires explicit disposition; preserve original-C zero AP |
| Turn report (turn.go:781–805; C doturn.c:767–782) | `groundAssaults[A][D][star]`, races/names/star | Counter reset0; aggregated combat news | Counter is absent from WorldCommit/loadAll and is lost on restart; news is written atomically only with a successful turn | Counter is historical event data, **not derivable** from final planet/race/ship records; no gameplay-strength consumer was found |

No human movement/ship-unload assault directly changes stockpiles, tax, terrain,
resources, crystals, MaxPopn or credentials. Such fields must remain unchanged
in a closed operation. Mechanical destruction may widen the ship/race/context
set; it must never be treated as an absent defender or an implicit allocator
operation. Transporter effects remain #42, not an assault shortcut.

## Fresh deterministic current-master reproduction

The reference harness links **unmodified source/move.c and source/rand.c**.
Small helper bodies are extracted verbatim from mobiliz.c, fire.c, shlmisc.c
and GB_server.c. In-memory get/put adapters model only disposable fixture I/O;
notification adapters record output. Unreached engine callbacks abort with99.
The harness's putrace is a no-op: its morale/Points observations are the actual
local C race values, not evidence of independently durable C race records.
No AFV exists in this particular reference fixture; mechanical behavior is
audited separately, not mocked as safe. All raw inputs/build commands/helper
text and hashes are retained in the evidence archive.

The fixture replaces libc random() with the valid constant2147483647. The Go
audit uses an external compiler overlay with corresponding double/integer
results, on a **test binary only**. No production RNG or environment behavior is
changed. Positive nonzero strengths avoid C undefined arithmetic in the capture
fixture. The regression invokes actual State.HandleCommand, normal dispatch,
and real bbolt records; it does not call groundAttack directly as a substitute.

| Defined case | Original C execution | Current Go real actor |
| --- | --- | --- |
| `move 0,0 6 1000`, attacker Fighters10, defender1civilian/Fighters1 | No attacker casualties; defender eliminated; target Owner1/Popn1000; evacuated origin; AP92; morale+1/-1; winner Points[2]+1 | All1000 return to origin; target Owner0/Popn0; falsely reports repulse; AP92 in actor versus100 stored; morale-10/+10 |
| `deploy 0,0 6 1000`, otherwise same | Target Owner1/Troops1000, origin empty; AP95; morale+1/-1; Points[2]+1 | All1000 troops return; target neutral; actor AP95 versus100 stored; same wrong morale direction |
| Failure after separate race writes, before planet persistence | No atomic command contract claimed for historical C | Existing registry warning callback raises the real write fence after the race saves. Races persist changed, stored defender remains Owner2/Popn1, actor target is neutral, actor AP debited. Legacy command returns without a checked write error; later turn flushes the changed actor planet |

All four civilian/military × normal/fenced regression leaves fail the desired
C capture assertions. Snapshots cover before, after command, segment, update
and graceful shutdown/restart. Subsequent turns do not turn the neutral target
into a successful conquest; legitimate growth/maintenance changes are recorded
without requiring unchanged mutable gameplay fields.

A private **causality probe**, not a proposed implementation, inserts only the
missing defender-elimination Owner assignment in an external Go overlay. Both
unfenced capture assertions then pass. This does not make persistence, AP,
accounting, messages or edge cases safe, and is not installed in the repository.

Separately, current hostile ship-unload dispatch passes its no-write guard test.
A second external overlay bypasses only that guard to investigate the dormant
legacy path through actual dispatch. It captures correctly in the stored planet
(Owner1/Popn1000) and empties the selected ship, while the actor still sees
Owner2/Popn1. Thus the two paths have **different** gameplay and persistence bugs.
The guard remains unchanged and enabled in current source.

## Defined conflicts and undefined cases requiring disposition

1. **Mutual destruction is not ordinary occupation.** A real C one-versus-one
   execution announces `VICTORY! The sector is yours!`, grants attacker morale
   and points, then leaves both sectors neutral/empty. The deferred C ship
   helper instead leaves the empty sector owned by the attacker. No new rule
   deciding tactical victory versus occupied capture is silently chosen here.
2. **Accounting differs from an immediately coherent world.** Movement leaves
   NumSectsOwned and planet/per-owner resident totals unchanged even after
   casualties/capture. The C no-survivor `done` path does not persist its modified
   planet Info at the final loop exit. #41A's owner-approved two-case correction
   is precedent, not blanket approval for newly discovered combat semantics.
3. **Empty foreign sectors produce zero defense.** ground_attack divides by
   strength without guarding0 (move.c:839–865). No undefined C result can be an
   oracle for legal occupation of an empty-owned sector. Legacy Fighters0 and
   other malformed/nonfinite combat states need explicit validation too.
4. **Absorption can exceed uint16 sector capacity.** Accumulating survivors and
   bodies in promoted widths is necessary; wrapping is forbidden. Rejecting,
   limiting or redistributing an otherwise possible outcome has different
   gameplay implications. No cap or altered roll distribution is invented.
5. **Mechanical casualty/AP ordering is inconsistent.** C and Go screen with
   AFVs before origin subtraction/AP admission. All-killed parties can remain
   untouched at origin; partial screening removes only surviving departures.
   Fixing conservation and refusing all effects on insufficient AP needs a
   clearly approved correction. Mechanical destruction also reaches child/peer
   ship effects in C that the current Go killShip does not reproduce.
6. **Ship retreat mass:** C load.c:1130 subtracts returned survivors' mass again;
   Go:992 adds it. Preserve correct Go mass conservation rather than copying
   that C typo only after explicit owner disposition for this newly migrated
   assault path. This decision is not needed to implement sector-origin combat
   if ship assault is separately deferred.

Other defined parity work is straightforward but still unimplemented: restore
C winner Points using its actual `loser.Playernum` index, preserve C negative
movement amounts, promote sum comparisons and check every arithmetic result.
Do not globally change unrelated combat helpers to obtain this slice.

The proposed first-slice outcome table below is **for owner disposition, not
implemented or previously approved gameplay**. Normal defined casualty formulas
and draw distributions would remain unchanged.

| Outcome after the defined casualty/absorption calculation | Proposed final rule |
| --- | --- |
| Defenders remain | Repulse; retain defender ownership, return surviving attackers, apply defined defender-win morale/Points and checked absorption |
| Defenders eliminated; attacking residents (survivors plus valid absorbed bodies) remain | Assign attacker ownership, place those residents, apply defined attacker-win morale/Points; announce capture only after commit/install |
| Both forces eliminated; no attacking residents after absorption | Neutral empty target; report mutual destruction, no occupied-capture claim and no winner morale/Points. This intentionally differs from C's victory award and needs approval |
| Foreign-owned sector was empty before the command | Uncontested occupation by the arriving party without dividing by zero or consuming human-combat casualty draws; preserve hostile-movement AP/admission and learning/attempt accounting, but no combat-win morale/Points. This defines previously undefined C behavior and needs approval |
| Defined absorption or an aggregate/counter result cannot fit its model field | Reject the entire proposal before persistence, no installed casualties/AP/output, no automatic reroll. Do not cap, wrap or redistribute bodies |
| Invalid nonfinite/zero combat strength other than the empty-target rule | Reject before consuming combat draws; do not convert NaN/infinity or invent a zero-Fighters combat formula |

For every approved outcome, derive planet/per-owner population, troops,
NumSectsOwned and MobPoints from actual before/after sector transitions using
checked promoted arithmetic. Do not transfer mobilization or ownership counts
merely because the defender was killed. This is an explicit extension of the
#41A transition-accounting principle, not a claim that C already maintains these
totals during combat. Mechanical and ship-retreat rules remain separate decisions.

## Feasibility: no blanket architecture blocker

Existing record families can hold a complete bounded assault. A dedicated
closed transaction could accept typed expected/proposed P, A/D and S (plus
selected ship for unloading, mechanical ships/races where supported), exact
scope/context and validated draw tape. It must independently recompute the
allowed result, whitelist all fields, compare full persisted preimages within
one bbolt transaction, append required existing-format messages there, and use
the existing command mutex, fence/maintenance check and uncertainty guard.
PopulationTransfer/CommandRecords remain closed to their existing operations.
No new commit kind or per-record stamp is needed for this proposed operation.

Immutable witnesses include the full source planet/star and both races; active
governor and star control; directory; complete orbital/landing and ship-chain
membership; every parent/peer/child used; config/rule inputs; source quantities;
AP-bearing star (and U/root data when actually used). Read-only witnesses must
not be rewritten. All actor addresses/preimages must be rechecked before
coherent installation; outputs and other-player effects remain staged until
confirmed commit/install. Uncertain writes HALT without stale turn/shutdown
flushes. No automatic reroll, retry, reopen or reconciliation is proposed.

**Randomness can be staged without new persistent RNG state.** Call existing
global helpers once on the actor, record typed results/ranges, and run pure
replay for the storage validator. Validate exact draw order, bounds, finite
values and exhaustion; intRand's `hi<=low` branch consumes no random value,
while nonnegative roundRand consumes a double even for integral inputs. The
validator must never call global RNG or arbitrary gameplay callbacks. Abort
does not automatically rebuild the proposal. This preserves existing draw
distributions; it does not promise replay across restart or exactly-once text
commands. Migration-plan8.8's persisted ChaCha8 is future, unapproved scope.

**Telegrams fit existing storage, but delivery policy matters.** store.go:594
already has pushTelegramTx; a closed assault transaction can append to the
existing inbox bucket atomically with world/AP changes. There is no schema
obstacle for actual offline telegrams. Current warn is live-first, governor0
fallback, then independently stored if not delivered. CR-18's staged online
decision is best-effort live delivery, not a durable outbox. An online decision
followed by postcommit disconnect/crash can lose a warning unless the inbox is
also written. Always storing it changes online inbox/duplicate behavior.

## Smallest reviewable next design / owner decisions

| Decision | Recommended next step | Alternative / persistence impact |
| --- | --- | --- |
| First reviewed sub-slice | Explicit one-step, sector-origin human assaults; reject any longer assault invocation or eligible mechanical-defender case **before** prefix mutation, rolls or output. Ship assaults and AFVs remain named follow-ups | Full multi-step/AFV support is possible but requires all dependent records, causal combat rules and bounded complete effects; no arbitrary round cap or skipped defender |
| Capture/empty/mutual/absorption/accounting rules | Approve a precise conservation-based outcome table, actual final ownership-transition accounting and checked overflow policy before code; keep normal defined C victory/repulse formulas | Literal C includes contradictory ownership messages, stale counts and undefined arithmetic; cannot claim both literal parity and repaired invariants |
| Pending assault reporting | If durable reporting is required without a schema, authorize immediate per-assault NewsCombat records in the same transaction, with no duplicate future aggregate for those staged assaults | Explicitly retain historical volatile next-turn reporting and disclose crash loss; OR approve versioned pending-count metadata and atomic turn drain. New metadata is a schema decision, not implemented |
| Defender warning guarantee | If every warning must survive commit-before-delivery crashes, approve always-durable existing inbox records, with optional postcommit live hint and documented duplicate/read behavior | Preserve explicitly best-effort online delivery plus atomic offline inbox; OR separately approve outbox/delivery protocol. No outbox is invented here |
| Ship mass and mechanical corrections | Resolve the identified mass/casualty/AP/recursive-death differences before enabling their separate sub-slices | Keep their explicit refusal/deferment; do not route supported hostile requests through unsafe legacy writers |

If preserving lossless **next-turn aggregation** is required, the smallest
additional persistence proposal is a strict versioned namespaced pending-count
record in the existing meta bucket (attacker, defender, star, promoted count).
Assault commit checks/updates it atomically with records/messages; startup
restores it; the turn atomically drains it while appending aggregate news.
No new bucket/commit kind is needed, but this is still a new persisted format
and a CommitWorld contract change requiring separate approval. Older readers
ignore additive meta but cannot drain/preserve its semantics; rollback and
WorldDigest coverage must be independently tested. This design is **not coded**.

## Review, evidence and stopping status

A separate read-only reviewer audited both C/Go paths and storage/effect/RNG
mechanisms. It agrees that bbolt plus typed proposals and a draw tape can fit
bounded combat; complexity alone is not a blocker. It identified the concrete
edge/durability choices above and did not approve an invented rule or schema.
The review is a Phase0 assessment, not approval of nonexistent production code.
A fresh separate [evidence review](evidence/cr17c-ground-assault-review.md)
independently inspected both recorded execution histories and source/test
mechanisms. It passes Phase0 evidence accuracy, leaves the gameplay decisions
blocking, and explicitly does not claim reviewer re-execution or implementation
approval. Its C local-versus-durable race clarification was incorporated above;
the original review is preserved unchanged in the archive.

Raw C and Go executions, deterministic overlays, desired regression fixture,
sanitized full actor/store planet and race summaries, private causality probe,
initial C link failure and resolved fail-loud stubs are preserved with SHA-256
in the accompanying audit evidence. The initial JSON extraction assumption
(one log frame per large JSON message) failed and was corrected by concatenating
Go test output frames; no test result was changed or hidden.

The desired regression fixture is archived as `.go.txt`, not a failing test
installed into normal CI. It can become a permanent executable regression when
approved rule/RNG staging is implemented. No original C, production Go, tests,
CI/gate tooling, allowances, lint baseline or PR77 fixture optimization changes.
New assault SIGKILL/atomicity/full-suite acceptance is **not executed or claimed**:
there is no approved implementation to test. The accepted #41A regressions and
24 SIGKILL cases are separately re-executed as baseline compatibility evidence.
No commit, push, PR, issue closure, merge, deployment or live operation is made
while the mandatory owner-decision boundary is unresolved.

## Fresh execution results

| Execution | Result | Measured duration / count |
| --- | --- | --- |
| Controlled original-C civilian, military and mutual-destruction reference | PASS (harness executed defined inputs; observed historical contradictions retained) | Three executions; each <0.01s |
| Real-dispatch desired capture regression on unchanged Go | EXPECTED FAIL | Four failing civilian/military × fence leaves; game package4.538s; hostile-unload guard passed |
| Missing-Owner-only diagnostic overlay | PASS for the two unfenced capture assertions | game package3.761s; does not assert atomicity |
| Dormant hostile-unload diagnostic overlay | PASS for observation assertions | game package3.121s; current stock guard remains enabled |
| Accepted population and immutable-fixture regressions, `-count=1 -race` | PASS | 355 test nodes,333 leaves across rules21/db111/game223 nodes; no skipped nodes;24 real SIGKILL leaves. game20.109s, total runner25.737s |
| `make -C test/ctool check` | PASS | All existing fixtures reproduce exactly;1.168s |

The portable replay script was then executed end to end in a fresh temporary
output directory. It reproduced the same four expected capture failures, the
two passing causality probes and all355 accepted regression nodes, including
all24 real SIGKILL boundaries again. Both execution histories are retained;
this is repeated baseline evidence, not new assault crash acceptance.

Counts are Go JSON test nodes, not independent gameplay scenarios. The accepted
crash child entry returns normally outside its child environment; the24 named
process-crash boundary leaves, not that entry, are the SIGKILL evidence. No full
normal/race/long-suite runtime or600-second headroom claim is made for this
Phase0-only work. Full acceptance gates and new assault crash cases await an
approved implementation. The additive documentation/evidence has no protected
tooling, inventory or lint-baseline delta and requires no acknowledgment label.

Artifacts:

- [Execution ledger](evidence/cr17c-ground-assault-validation.json): exact
  commands, statuses, timings, counts, source fingerprints and output SHA-256.
- [Reproduction/evidence archive](evidence/cr17c-ground-assault-phase0.tar.gz):
  both raw execution histories, sanitized actor/store snapshots, C harness and
  helper text, desired `.go.txt` regression, private compiler overlays, portable
  `replay-audit.py`, both independent reviews and per-member `SHA256SUMS`.
  Archive SHA-256: `020cc7b69041df478da573c223686fc60c0dbb435f538d443aa0f067f58daf5f`.
- [Independent evidence review](evidence/cr17c-ground-assault-review.md):
  SHA-256 `dae11d8073eaa9566578c3298fb7bc5d1f15ed4b7246deb116fa8cef0c76d4c5`.

All archive member hashes were verified after creation. No binary, disposable
database, credential, cache or original-source alteration is included.

## Subsequent owner approval / implementation continuation

The owner subsequently approved the bounded outcome, accounting, absorption and
immediate durable news/inbox contract. The historical Phase 0 BLOCKED verdict
above records the earlier approval boundary, not the current authorization.
See [the implementation record](cr17c-ground-assault.md) for the approval and
subsequent source/test/review results. The original evidence archive, ledger and
independent Phase 0 reports remain unchanged.

The approved one-step nonmechanical implementation and fresh acceptance now
continue in that record. The historical overflow-rejection proposal above was
superseded by the owner's explicit capacity-bounded absorption decision; migrated
reporting uses approved atomic per-assault news/inbox records, not new metadata.
The original Phase0 archive/ledger/review hashes remain unchanged. Its diagnostic
replay must use the exact clean baseline source, not a modified worktree whose
HEAD alone still names that baseline. New causal controls validate the migrated
operation independently of the historical diagnostic overlays.
