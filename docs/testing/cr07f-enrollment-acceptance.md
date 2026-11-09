# CR-07F enrollment acceptance

Planning baseline: `7556c6c5a64694ee2af42730442c5369a3169db2`.
Dedicated worktree `/tmp/GB-cr07f`, branch
`feature/cr07f-enrollment-acceptance`. No live environment is authorized.

## Matrix established before test implementation

This matrix was created before writing CR-07F code. E = existing coverage to
execute freshly; N = new coverage needed after source/test inspection. Prior
reports and inspection are not execution evidence. BLOCKED initially means
fresh execution is pending; it is not a claim of an implementation defect.
The final columns now record fresh execution; the committed evidence ledger
below records commands, outcomes and source hashes. The initial pending matrix
was preserved in the work session before any test implementation.

Full C-derived state is verified in layers: actual in-process actor plus its
store, and complete persisted entities from real CLI/HTTP subprocess enrollment.
Real WebSocket commands verify those subprocess actors' home/government views;
no private model snapshot/debug endpoint is exposed.

| ID / original requirement | Component | Automated test (E/N) | Fresh evidence | Status | Limits/gap before coding |
| --- | --- | --- | --- | --- | --- |
| A1 audit G1, WU-A defined-C arithmetic/rounding | racegen | E [TestOriginalCCosts](../../go/internal/racegen/golden_test.go#L10); [TestOriginalCRounding](../../go/internal/racegen/golden_test.go#L52) | U + R (fresh, see ledger) | PASS | C fixture reproduction also required |
| A2 WU-A covariance, trait extrema and metadata | racegen | E [TestOriginalCMetadata](../../go/internal/racegen/golden_test.go#L61); [TestNumericBoundaries](../../go/internal/racegen/rules_test.go#L99) | U + R (fresh, see ledger) | PASS | Defined C only |
| A3 owner Q4 eight-type 600 extension/overflow | racegen | E [TestApprovedEightTypeExtensionAndOverflow](../../go/internal/racegen/rules_test.go#L138) | U + R (fresh, see ledger) | PASS | Explicit departure from undefined C |
| A4 full spec, invalid/NaN/Inf/ranges/cross-fields/no mutation | racegen | E [TestValidation](../../go/internal/racegen/rules_test.go#L21); [TestNumericBoundaries](../../go/internal/racegen/rules_test.go#L99); [TestPureRepeatableAndMetadataIsolation](../../go/internal/racegen/rules_test.go#L174); [TestSpecJSONAndNumericTypes](../../go/internal/racegen/rules_test.go#L211) | U + R (fresh, see ledger) | PASS | Browser never authoritative |
| B1 audit P2 seven home types/mappings | placement | E [TestPlacementAllHomeworlds](../../go/internal/game/placement_test.go#L156) | U + R (fresh, see ledger) | PASS | Original built GB_racegen.c oracle |
| B2 temperature -200/+100 and outside | placement | E [TestPlacementSearchRules](../../go/internal/game/placement_test.go#L184) | U + R (fresh, see ledger) | PASS | Inclusive owner-approved window |
| B3 single-planet/inhabited stars, stale ownership | placement | E [TestPlacementSearchRules](../../go/internal/game/placement_test.go#L184) | U + R (fresh, see ledger) | PASS | Stronger vacancy checks approved |
| B4 full permutations, invalid indices/no global RNG | placement/actor | E [TestPlacementFailuresLeaveInputsUnchanged](../../go/internal/game/placement_test.go#L294); [TestActorEnrollmentOrderingAndPolicyDisabled](../../go/internal/game/enrollment_test.go#L1176) | U + R (fresh, see ledger) | PASS | Isolated crypto/rand in production |
| B5 no candidate/no suitable terrain, occupied capital | placement | E [TestPlacementCapitalAndSpecies](../../go/internal/game/placement_test.go#L227); [TestActorEnrollmentNoHomeOrCapital](../../go/internal/game/enrollment_test.go#L454) | U + R (fresh, see ledger) | PASS | No (0,0) fallback |
| I1 audit2.3 append-only player/name, cap63 | actor/store | E [TestActorEnrollmentCapacityAndHistoricalSlots](../../go/internal/game/enrollment_test.go#L868); N [TestCR07FActorInitialEmpires](../../go/internal/game/enrollment_acceptance_test.go#L14) | U + R (fresh, see ledger) | PASS | No historical slot reuse |
| I2 home/default scope, active governor0/toggles, governors0 | placement/actor/store | E [TestPlacementInitialFields](../../go/internal/game/placement_test.go#L101); N [TestCR07FActorInitialEmpires](../../go/internal/game/enrollment_acceptance_test.go#L14); [TestCR07FGeneratedUniverseJourney](../../go/test/e2e/enrollment_acceptance_test.go#L428) | U + R (fresh, see ledger) | PASS | No hardcoded historical password0 |
| I3 distinct race/governor bcrypt, treasury0, others inactive | authority/actor/store | E [TestProvisionAndImmutableAuthority](../../go/internal/operatorenroll/authority_test.go#L15); N [TestCR07FGeneratedUniverseJourney](../../go/test/e2e/enrollment_acceptance_test.go#L428); [TestCR07FActorInitialEmpires](../../go/internal/game/enrollment_acceptance_test.go#L14) | U + R (fresh, see ledger) | PASS | Separate protected-file CLI inputs needed |
| I4 tech/morale/turn/discoveries/war/allied/points0 | actor/store | E [TestPlacementInitialFields](../../go/internal/game/placement_test.go#L101); N [TestCR07FActorInitialEmpires](../../go/internal/game/enrollment_acceptance_test.go#L14); [TestCR07FGeneratedUniverseJourney](../../go/test/e2e/enrollment_acceptance_test.go#L428) | U + R (fresh, see ledger) | PASS | Whole-model zero defaults |
| I5 conditions0..9 from planet; toxic0 | actor/store | E [TestPlacementInitialFields](../../go/internal/game/placement_test.go#L101); N [TestCR07FActorInitialEmpires](../../go/internal/game/enrollment_acceptance_test.go#L14); [TestCR07FGeneratedUniverseJourney](../../go/test/e2e/enrollment_acceptance_test.go#L428) | U + R (fresh, see ledger) | PASS | Not arbitrary environmental defaults |
| I6 translate100 self/1 others; all100 first/God | actor/store | E [TestPlacementCapitalAndSpecies](../../go/internal/game/placement_test.go#L227); N [TestCR07FActorInitialEmpires](../../go/internal/game/enrollment_acceptance_test.go#L14); [TestCR07FGeneratedUniverseJourney](../../go/test/e2e/enrollment_acceptance_test.go#L428) | U + R (fresh, see ledger) | PASS | Safe indexing, no C out-of-bounds store |
| I7 all traits, IQ vs IQ_limit, Fertilize×100 | actor/store | E [TestPlacementCapitalAndSpecies](../../go/internal/game/placement_test.go#L227); N [TestCR07FActorInitialEmpires](../../go/internal/game/enrollment_acceptance_test.go#L14); [TestCR07FGeneratedUniverseJourney](../../go/test/e2e/enrollment_acceptance_test.go#L428) | U + R (fresh, see ledger) | PASS | Full racegen.Spec fields |
| I8 likes fractions/last common100 LikesBest | placement/actor/store | E [TestPlacementCapitalAndSpecies](../../go/internal/game/placement_test.go#L227); N [TestCR07FActorInitialEmpires](../../go/internal/game/enrollment_acceptance_test.go#L14); [TestCR07FGeneratedUniverseJourney](../../go/test/e2e/enrollment_acceptance_test.go#L428) | U + R (fresh, see ledger) | PASS | No C++ enhancements |
| I9 capital owner/race, sexes population, fertility100/efficiency10/troops0 | actor/store | E [TestPlacementInitialFields](../../go/internal/game/placement_test.go#L101); N [TestCR07FActorInitialEmpires](../../go/internal/game/enrollment_acceptance_test.go#L14); [TestCR07FGeneratedUniverseJourney](../../go/test/e2e/enrollment_acceptance_test.go#L428) | U + R (fresh, see ledger) | PASS | Preserve geology/resources/crystals |
| I10 planet popn/troops/MaxPopn original1100×width×height/2 | actor/store | E [TestPlacementInitialFields](../../go/internal/game/placement_test.go#L101); N [TestCR07FActorInitialEmpires](../../go/internal/game/enrollment_acceptance_test.go#L14); [TestCR07FGeneratedUniverseJourney](../../go/test/e2e/enrollment_acceptance_test.go#L428) | U + R (fresh, see ledger) | PASS | Planet.Info.Popn remains C default0 initially |
| I11 Planet.Info owned1/exploredtrue, global exploredfalse, shiphead | actor/store | E [TestPlacementInitialFields](../../go/internal/game/placement_test.go#L101); N [TestCR07FActorInitialEmpires](../../go/internal/game/enrollment_acceptance_test.go#L14); [TestCR07FGeneratedUniverseJourney](../../go/test/e2e/enrollment_acceptance_test.go#L428) | U + R (fresh, see ledger) | PASS | No invented stockpiles/inventories |
| I12 home-star explored/inhabited bit/AP5, global AP unchanged | actor/store | E [TestPlacementInitialFields](../../go/internal/game/placement_test.go#L101); N [TestCR07FActorInitialEmpires](../../go/internal/game/enrollment_acceptance_test.go#L14); [TestCR07FGeneratedUniverseJourney](../../go/test/e2e/enrollment_acceptance_test.go#L428) | U + R (fresh, see ledger) | PASS | Other races/stars preserved |
| I13 government ship all equipment/capacities/size/cost/flags | actor/store | E [TestPlacementInitialFields](../../go/internal/game/placement_test.go#L101); N [TestCR07FActorInitialEmpires](../../go/internal/game/enrollment_acceptance_test.go#L14); [TestCR07FGeneratedUniverseJourney](../../go/test/e2e/enrollment_acceptance_test.go#L428) | U + R (fresh, see ledger) | PASS | Full literal, not constructor output |
| I14 ship mass/base/crew/type/buildtype/owner/governor | actor/store | E [TestPlacementInitialFields](../../go/internal/game/placement_test.go#L101); N [TestCR07FActorInitialEmpires](../../go/internal/game/enrollment_acceptance_test.go#L14); [TestCR07FGeneratedUniverseJourney](../../go/test/e2e/enrollment_acceptance_test.go#L428) | U + R (fresh, see ledger) | PASS | Mass100+10×species mass |
| I15 ship coordinates/landing/orbit chain/Race.GovShip | actor/store | E [TestPlacementOrbitChains](../../go/internal/game/placement_test.go#L414); N [TestCR07FActorInitialEmpires](../../go/internal/game/enrollment_acceptance_test.go#L14); [TestCR07FGeneratedUniverseJourney](../../go/test/e2e/enrollment_acceptance_test.go#L428) | U + R (fresh, see ledger) | PASS | Existing chain prepended safely |
| I16 governed() and initial home/government commands | actor/HTTP/WS | E [TestActorEnrollmentSecurityAndCommandVisibility](../../go/internal/game/enrollment_test.go#L796); N [TestCR07FActorInitialEmpires](../../go/internal/game/enrollment_acceptance_test.go#L14); [TestCR07FGeneratedUniverseJourney](../../go/test/e2e/enrollment_acceptance_test.go#L428) | U + R (fresh, see ledger) | PASS | Unrelated LevelStar predicate defect remains |
| F1 fresh DB, deterministic universe init through gbctl, SERVING | real subprocess/CLI | N [TestCR07FGeneratedUniverseJourney](../../go/test/e2e/enrollment_acceptance_test.go#L428) | U + R (fresh, see ledger) | PASS | E prewrites a normalized fixture instead |
| F2 protected-file credential CLI, universe-bound manifest/restart | real subprocess/CLI | N [TestCR07FGeneratedUniverseJourney](../../go/test/e2e/enrollment_acceptance_test.go#L428) | U + R (fresh, see ledger) | PASS | E uses library provisioning/reused pair |
| F3 God-first explicit authorization and permission denial | provider/actor/CLI | E [TestCR07EOperatorEnrollmentAndDualLogin](../../go/test/e2e/enrollment_test.go#L26); N [TestCR07FGeneratedUniverseJourney](../../go/test/e2e/enrollment_acceptance_test.go#L428) | U + R (fresh, see ledger) | PASS | Bootstrap disabled by default |
| F4 two distinct accounts/players/homeworlds/ships, immediate actor publication | real subprocess/actor | E [TestActorEnrollmentConcurrentRequests](../../go/internal/game/enrollment_test.go#L1263); N [TestCR07FGeneratedUniverseJourney](../../go/test/e2e/enrollment_acceptance_test.go#L428) | U + R (fresh, see ledger) | PASS | Both races must exist before turn/restart |
| F5 confirmed durable placement, same actor/store full state | real actor/store | E [TestActorEnrollmentCompletePlacement](../../go/internal/game/enrollment_test.go#L246); N [TestCR07FActorInitialEmpires](../../go/internal/game/enrollment_acceptance_test.go#L14); [TestCR07FGeneratedUniverseJourney](../../go/test/e2e/enrollment_acceptance_test.go#L428) | U + R (fresh, see ledger) | PASS | Presence/count alone insufficient |
| L1 separate credential HTTP/JWT/WS welcome and cs/survey/map/capital for both | real net/dispatcher | E [TestCR07EOperatorEnrollmentAndDualLogin](../../go/test/e2e/enrollment_test.go#L26); N [TestCR07FGeneratedUniverseJourney](../../go/test/e2e/enrollment_acceptance_test.go#L428) | U + R (fresh, see ledger) | PASS | Silent cs verified by following survey |
| L2 wrong race/governor credentials, inactive/index abuse/legacy bypass | real net/actor | E [TestCR07EOperatorEnrollmentAndDualLogin](../../go/test/e2e/enrollment_test.go#L26); [TestEnrollmentCurrentSessionAuthority](../../go/internal/game/enrollment_interfaces_test.go#L22) | U + R (fresh, see ledger) | PASS | Generic denials, no governor fallback |
| L3 legitimate historical legacy HTTP and actor login | real net/actor | E [TestAcceptanceScenario](../../go/test/e2e/basic_test.go#L351); [TestLegacyLoginAndStrictEnrollmentHistory](../../go/internal/game/enrollment_interfaces_test.go#L97) | U + R (fresh, see ledger) | PASS | New races require dual login |
| L4 JWT/WS permission/governor revocation | real net/actor | E [TestEnrollmentCurrentSessionAuthority](../../go/internal/game/enrollment_interfaces_test.go#L22) | U + R (fresh, see ledger) | PASS | Existing Go gb-client remains legacy-only |
| T1 update and movement segment, both still governed/valid | real subprocess/store/actor | N [TestCR07FGeneratedUniverseJourney](../../go/test/e2e/enrollment_acceptance_test.go#L428) | U + R (fresh, see ledger) | PASS | Mutable gameplay fields may change |
| T2 schedule and commit seq/kinds, graceful restart and both re-login | real subprocess/store | N [TestCR07FGeneratedUniverseJourney](../../go/test/e2e/enrollment_acceptance_test.go#L428) | U + R (fresh, see ledger) | PASS | init/update/segment/shutdown only |
| T3 CR43 quiesce, restart QUIESCED, refusal, resume | subprocess/maintenance | E [TestQuiesceSurvivesCrashAndRestart](../../go/test/e2e/maintenance_test.go#L74); N [TestCR07FGeneratedUniverseJourney](../../go/test/e2e/enrollment_acceptance_test.go#L428) | U + R (fresh, see ledger) | PASS | Disposable server only |
| R1 immediate/after-turn/after-restart replay original placement | real admin/CLI | E [TestCR07EOperatorEnrollmentAndDualLogin](../../go/test/e2e/enrollment_test.go#L26); N [TestCR07FGeneratedUniverseJourney](../../go/test/e2e/enrollment_acceptance_test.go#L428) | U + R (fresh, see ledger) | PASS | No old proposal reconstruction |
| R2 timeout after actor started, safely queryable retry | real private HTTP/actor | E [TestPrivateEnrollmentTimeoutAndUncertainty](../../go/internal/admin/enrollment_integration_test.go#L28) | U + R (fresh, see ledger) | PASS | Timeout is UNKNOWN, not rollback |
| R3 changed spec / other caller same ID | actor/admin/CLI | E [TestActorEnrollmentIdentityConflicts](../../go/internal/game/enrollment_test.go#L374); N [TestCR07FGeneratedUniverseJourney](../../go/test/e2e/enrollment_acceptance_test.go#L428) | U + R (fresh, see ledger) | PASS | Exact account binding |
| R4 same account new ID/case-insensitive name duplicate | actor/admin/CLI | E [TestActorEnrollmentIdentityConflicts](../../go/internal/game/enrollment_test.go#L374); N [TestCR07FGeneratedUniverseJourney](../../go/test/e2e/enrollment_acceptance_test.go#L428) | U + R (fresh, see ledger) | PASS | No name-based bypass |
| R5 invalid/rebound/changed credential version | provider/actor/CLI | E [TestProvisionAndImmutableAuthority](../../go/internal/operatorenroll/authority_test.go#L15); [TestActorEnrollmentChangedCredentialReference](../../go/internal/game/enrollment_test.go#L1336); N [TestCR07FGeneratedUniverseJourney](../../go/test/e2e/enrollment_acceptance_test.go#L428) | U + R (fresh, see ledger) | PASS | Preserve immutable old versions |
| R6 mismatched universe and bootstrap-disabled historical God retry | real provider/CLI | E [TestCR07EOperatorEnrollmentAndDualLogin](../../go/test/e2e/enrollment_test.go#L26); N [TestCR07FGeneratedUniverseJourney](../../go/test/e2e/enrollment_acceptance_test.go#L428) | U + R (fresh, see ledger) | PASS | No account/game claims trusted from JSON |
| R7 evolved planet/renamed race/destroyed ship replay | actor/store | E [TestActorEnrollmentRetryAfterScheduledTurnAndDestruction](../../go/internal/game/enrollment_test.go#L316); [TestActorEnrollmentRetryDoesNotPrepareOrSearch](../../go/internal/game/enrollment_test.go#L358) | U + R (fresh, see ledger) | PASS | Historical placement identity, not current survival |
| C1 pre-operation process death | subprocess/store | N [TestCR07FEnrollmentCrashWindows](../../go/test/e2e/enrollment_crash_test.go#L111) | U + R (fresh, see ledger) | PASS | Inspect all durable evidence |
| C2 SIGKILL in open transaction/after writes before commit | subprocess/bbolt | N [TestCR07FEnrollmentCrashWindows](../../go/test/e2e/enrollment_crash_test.go#L111) | U + R (fresh, see ledger) | PASS | Build-tag-only hook, no route |
| C3 commit then death before HTTP response, restart/replay | subprocess/bbolt/actor | N [TestCR07FEnrollmentCrashWindows](../../go/test/e2e/enrollment_crash_test.go#L111) | U + R (fresh, see ledger) | PASS | Verify complete committed placement |
| C4 rollback/errors/panics every write stage, one transaction | store/actor | E [TestEnrollmentRollbackFaultStages](../../go/internal/db/enrollment_test.go#L198); [TestEnrollmentExactlyOneWriteTransaction](../../go/internal/db/enrollment_test.go#L996); [TestActorEnrollmentTransactionFailures](../../go/internal/game/enrollment_test.go#L568) | U + R (fresh, see ledger) | PASS | Compare buckets and free-list sequences |
| C5 confirmed commit reporting error/panic | store/actor/HTTP | E [TestEnrollmentCommitOutcomes](../../go/internal/db/enrollment_test.go#L237); [TestActorEnrollmentPostCommitAndUncertainty](../../go/internal/game/enrollment_test.go#L600); [TestPrivateEnrollmentTimeoutAndUncertainty](../../go/internal/admin/enrollment_integration_test.go#L28) | U + R (fresh, see ledger) | PASS | Never claim rollback |
| C6 uncertain commit/readback/verification fail, halt/no stale flush | store/actor | E [TestActorEnrollmentReadbackAndInstallFailures](../../go/internal/game/enrollment_test.go#L654); [TestActorEnrollmentUncertainStoreCannotBeReused](../../go/internal/game/enrollment_test.go#L1144); [TestEnrollmentStatusFailureEvidence](../../go/internal/game/enrollment_interfaces_test.go#L112) | U + R (fresh, see ledger) | PASS | No auto-repair/reopen/resume |
| C7 fence accounting/no partial allocator/placement | store | E [TestEnrollmentFence](../../go/internal/db/enrollment_test.go#L288); [TestEnrollmentFenceWhileWaitingToWrite](../../go/internal/db/enrollment_test.go#L1041); [TestEnrollmentFreeListAndDeadRecords](../../go/internal/db/enrollment_test.go#L393) | U + R (fresh, see ledger) | PASS | No unsafe dead-ID reclamation |
| Q1 simultaneous requests competing for homeworld | actor/store | E [TestActorEnrollmentConcurrentRequests](../../go/internal/game/enrollment_test.go#L1263); [TestEnrollmentConcurrentSlotsAndShips](../../go/internal/db/enrollment_test.go#L351) | U + R (fresh, see ledger) | PASS | One actor writer |
| Q2 two requests for final slot 63 | actor/store | N [TestCR07FFinalSlotCompetition](../../go/internal/game/enrollment_acceptance_test.go#L81) | U + R (fresh, see ledger) | PASS | Existing capacity test is sequential |
| Q3 login during scheduled turn / enrollment mode and queued turn transition | actor | E [TestAuthenticateThroughActor](../../go/internal/game/lifecycle_test.go#L538); [TestActorEnrollmentTurnSerialization](../../go/internal/game/enrollment_test.go#L777); N [TestCR07FTurnAdmission](../../go/internal/game/enrollment_acceptance_test.go#L236) | U + R (fresh, see ledger) | PASS | Deterministic barriers |
| Q4 queued cancellation/started timeout | actor | E [TestActorEnrollmentQueuedCancellationAndStartedTimeout](../../go/internal/game/enrollment_test.go#L721) | U + R (fresh, see ledger) | PASS | Buffered reply lifecycle |
| Q5 quiesce during enrollment and Stop while enrollment in flight | actor/store | N [TestCR07FEnrollmentQuiesceAndShutdown](../../go/internal/game/enrollment_acceptance_test.go#L140) | U + R (fresh, see ledger) | PASS | Ordered commit then maintenance/flush |
| Q6 HALTED/QUIESCED/STARTING/STOPPED/draining refusal | actor/HTTP | E [TestActorEnrollmentModesAndDraining](../../go/internal/game/enrollment_test.go#L698); [TestActorEnrollmentMaintenanceRetryGate](../../go/internal/game/enrollment_test.go#L1299) | U + R (fresh, see ledger) | PASS | No write on refusal |
| D1 CR17 pending AP disagreement, no-write SERVING, turn/retry | actor/real dispatcher | E [TestActorEnrollmentPendingCommandAPWaitsForTurn](../../go/internal/game/enrollment_test.go#L1416) | U + R (fresh, see ledger) | PASS | AP temporary admission condition |
| D2 non-AP stale world fail-closed/no overwrite | actor/store | E [TestActorEnrollmentPreexistingStoreDrift](../../go/internal/game/enrollment_test.go#L925); [TestActorEnrollmentStaleTransactionPreconditions](../../go/internal/game/enrollment_test.go#L983) | U + R (fresh, see ledger) | PASS | General CR17 persistence remains open |
| S1 public POST/enroll disabled even previewtrue; no private routes mounted | real net | E [TestOriginsAndPreview](../../go/internal/config/config_test.go#L218); N [TestCR07FGeneratedUniverseJourney](../../go/test/e2e/enrollment_acceptance_test.go#L428) | U + R (fresh, see ledger) | PASS | Public signup NOT authorized |
| S2 missing/bad admin tokens; unknown game/account, God/Guest independent | real net/provider | E [TestCR07EOperatorEnrollmentAndDualLogin](../../go/test/e2e/enrollment_test.go#L26); N [TestCR07FGeneratedUniverseJourney](../../go/test/e2e/enrollment_acceptance_test.go#L428) | U + R (fresh, see ledger) | PASS | One configured private operator principal |
| S3 protected file permissions/equal/too-long/bcrypt failure | CLI/provider/actor | E [TestCredentialCLIProtectedInputsAndNoSecretOutput](../../go/cmd/gbctl/enrollment_test.go#L14); [TestProvisionAndImmutableAuthority](../../go/internal/operatorenroll/authority_test.go#L15); [TestPreparationFailureAndGameBinding](../../go/internal/operatorenroll/authority_test.go#L96); [TestActorEnrollmentCredentialFailures](../../go/internal/game/enrollment_test.go#L491) | U + R (fresh, see ledger) | PASS | No plaintext fallback |
| S4 malformed/oversized/unknown/duplicate/null/sector arrays | wire/admin/net | E [TestStrictSpec](../../go/internal/wirejson/json_test.go#L10); [TestCR07EOperatorEnrollmentAndDualLogin](../../go/test/e2e/enrollment_test.go#L26) | U + R (fresh, see ledger) | PASS | No weakening of strict decoder |
| S5 no secrets across ALL process logs/CLI/public responses | real subprocess/net | N [TestCR07FGeneratedUniverseJourney](../../go/test/e2e/enrollment_acceptance_test.go#L428) | U + R (fresh, see ledger) | PASS | E checks final process logs only |
| S6 retired racegen cannot open/write DB | offline CLI | E [TestRetiredDatabaseFlagAndSpecRoundTrip](../../go/cmd/racegen/main_test.go#L107) | U + R (fresh, see ledger) | PASS | Designer remains offline |
| S7 normal binary excludes new crash hook | build boundary | N [TestCR07FProductionBuildIgnoresCrashHook](../../go/test/e2e/enrollment_crash_test.go#L213) | U + R (fresh, see ledger) | PASS | No public debug controls |
| V1 additive placement keys accepted by actual older reader | store/startup | E [TestEnrollmentCR07BAndCompatibleStartup](../../go/internal/db/enrollment_integration_test.go#L28) + exported readers | U + R (fresh, see ledger) | PASS | DATA compatibility only |
| V2 strict CR18 commit/schedule and CR43 maintenance | store/actor | E [TestEnrollmentCR07BAndCompatibleStartup](../../go/internal/db/enrollment_integration_test.go#L28); CR18/43 suites | U + R (fresh, see ledger) | PASS | No schema/migration/kind changes |
| V3 authentication/security rollback prerequisites | docs/ADR | independent acceptance review | reviewer + actual older-reader runs | PASS | Older readers lack new protections |
| G1 full format/build/vet/unit/race/inventory/C/golden/lint/growth/mutation gates | repository | fresh full verification commands | local command ledger | PASS | No required jobs disabled |
| G2 offline manifests/workflow policy | repository | fresh local commands | local command ledger | PASS | No cluster access |
| G3 hosted container builds/scans/SBOM/kind | hosted CI | existing unchanged ci.yml jobs | pending PR run | BLOCKED | External merge condition; no local runtime installed |
| O1 live-game/cluster/image/public exposure operations | operations | not executed | authorization excludes | NOT IN SCOPE | Separate owner authorization required |
| O2 identity registration/rate limits/bot defense/TypeScript apps/CR20/CR17 rewrite | future product | not implemented | authorization excludes | NOT IN SCOPE | Private enrollment is not public signup |
| O3 optional archetypes and C++ enhancements | future rules/UI | not implemented | authorization excludes | NOT IN SCOPE | Original C remains authority |

## Additions selected from the pre-code gaps

1. Generated-universe subprocess journey using actual initialization and credential
   CLI commands, independently provisioned accounts and complete initial-state
   store checks, both-race commands, update+segment+restart, denials and secret scans.
2. Full initial model assertions on the actor and store; final-slot competition;
   enrollment/quiesce/shutdown barriers; scheduled turn admission evidence.
3. Build-tag-only enrollment crash hook and real SIGKILL windows, including
   post-commit lost response; prove ordinary binaries ignore the hook.
4. Fresh execution of existing A–E, CR18/43 and compatibility tests, with a
   sanitized evidence ledger, enrollment ADR and precise closure/limitation notes.

The planning revision made no execution claim. Results below are from this
CR-07F working tree, not copied from earlier reports.

## Verdict and environment

Matrix summary: 75 rows; 71 PASS, one BLOCKED (hosted CI pending), three NOT IN
SCOPE. The 65 named matrix tests passed freshly in both full execution modes.

**CR-07F: PASS WITH CONDITIONS** for the implemented private operator/internal
enrollment objectives, subject to normal owner review and hosted CI. No core
enrollment defect was found. This accepts the enrollment subset of Phase 3c;
it does not complete that phase, the whole game, public signup or live rollout.
The G3 hosted row remains BLOCKED until the actual PR run completes.

Executed 2026-11-09 on Linux x86_64, Go 1.27.3, from master
`7556c6c5a64694ee2af42730442c5369a3169db2` plus this PR's test/documentation
changes. [The sanitized execution ledger](cr07f-execution-evidence.json) records
the exact changed Go/test source SHA-256 values, commands, package counts and
every matrix-referenced test's fresh outcome. This identifies the executed
source without a self-referential commit hash in the record. U = fresh full
non-race run; R = fresh full race run. All matrix-named automated tests passed
in both. Their executed subtests remain part of the full suites.

`GOCACHE=/tmp/cr07a-cache` reused compiled tools, not cached test outcomes
(`-count=1`). `TMPDIR=/tmp/GB-cr07f/test/ctool/obj/tmp` avoided this host's
invalid `/tmp/.git` marker during temporary-module VCS stamping. No test or VCS
check was disabled. Loopback tests required sandbox socket permission. Every
database, credential, process and simulated operator was disposable. Test
passwords were generated at runtime, held only in 0600 temporary artifacts and
removed by test cleanup; no real artifacts or raw secret-bearing logs are
committed.

## Fresh execution ledger

Commands run from the root unless prefixed `go -C go`; the cache/TMPDIR above
apply to Go and script commands. Local stdout/JSON logs were retained under
`/tmp/cr07f-*` during review; the committed ledger contains only sanitized
results, source hashes and test names, not complete model values or tokens.

| Check actually executed | Result |
| --- | --- |
| `gofmt -l go`; `go -C go build ./...`; `go -C go vet ./...` | PASS, formatting empty |
| `go -C go test -count=1 -json ./...` (U) | PASS, 3,133 tests/subtests passed, 2 opt-in tests skipped, 20 tested packages |
| `GB_LONG_TESTS=1 go -C go test -count=1 -race -json ./...` (R) | PASS, 3,135 tests/subtests, zero skips, 20 tested packages; includes both production-duration WS tests |
| `test/scripts/inventory-check.sh` | PASS, actual-execution reconciliation; existing explicit CR-18 entry unchanged |
| `make -C test/ctool check` | PASS, all committed C fixtures reproduce exactly |
| `test/scripts/golden-mutations.sh` | PASS, all 15 expected scenarios, including unchanged-tree inventory/OpenAPI validation |
| `test/scripts/lint-gate.sh /tmp/cr07f-lint-final.json` | PASS, 1,052 matched, zero new/stale; baseline unchanged |
| `PR_LABELS_JSON='[]' test/scripts/growth-gate.sh origin/master` | PASS, no allowance/protected-tooling changes |
| `deploy/kustomize/tests/run.sh --require-published-image` | PASS, structural/mutation/safety checks; 21 valid resources, zero invalid/errors/skips; offline only |
| `python3 -m unittest discover -s .github/scripts -p 'test_*.py'` | PASS, 184 tests including Syft recovery policy |
| `python3 .github/scripts/workflow_policy_check.py` | PASS, 17 assertions; workflow unchanged |
| Cached pinned `actionlint -no-color`; `shellcheck -S warning .github/scripts/*.sh` | PASS |
| Pinned `govulncheck@v1.9.0 ./...`; `go mod verify`; `go mod tidy -diff` | PASS,zero called vulnerabilities;17 unreached dependency-module advisories; no module changes |
| Existing golden-mutation helper, lint-gate and approval-gate self-tests | PASS, all expected outcomes; protected scripts unchanged |
| `git diff --check`; staged diff check | PASS |

Counts include named parent tests/subtests, not individual assertions. The
normal run's two intentional skips were freshly executed and passed in R with
`GB_LONG_TESTS=1`. Existing A–E, CR-18 commit failure/recovery and CR-43 durable
maintenance tests were rerun through U/R; earlier reports were not counted as
fresh evidence. Optional gate self-test results are listed in the JSON ledger.
Container builds, Trivy, Syft SPDX and kind are hosted-only merge conditions;
the PR uses the unchanged workflow including PR #29's pinned Syft retry logic.

## C-derived initial state and real transport journey

New coverage consists of seven top-level tests, 14 leaf cases and 17 test nodes
in `enrollment_acceptance_test.go` (game), `enrollment_acceptance_test.go` (e2e)
and `enrollment_crash_test.go`, plus independent assertions in
`go/test/acceptance/empire.go`. All earlier A–E tests are reused, unweakened.

The new journey starts an empty server, asserts UNINITIALIZED, then runs actual
`gbctl universe init` with seed 11, 32 stars and 2..3 planets. It checks SERVING,
reads the server's actual universe binding, shuts down and uses actual
`gbctl credentials prepare` on separate protected files for each account/version.
It configures/restarts with the private authority. Actual server-side policy
denies ordinary first creation, disabled bootstrap, unauthorized God and Guest;
after explicit bootstrap authorization it enrolls player 1 God and player 2
ordinary through gbctl. Different accounts receive different home stars and
ships, with immediate published counts. The universe seed is deterministic;
production star/capital selection remains isolated secure randomness. The test
checks every eligible Earth candidate has land rather than normalizing generated
planets or relying on a lucky search order.

The independent C oracle constructs whole Race/Star/Planet/Ship expectations,
including unchanged and zero-valued fields. It does not call placement or ship
constructors. It compares every model field and emits field names only.
Its source references are the built `GB_racegen.c:150–250,265–363`, government
row `shipdata.h:79` and `max.c:28–41`. Matrix I1–I16 exhaust the original audit
section 2.3 table: all attributes/conversions, environmental conditions, scopes,
active governor0/other governors, credentials, translation/zero treasury/tech/
morale, likes, population/ownership/exploration/AP, full ship equipment/capacity/
mass/location/flags, links and approximate MaxPopn. CR-07B's existing all-seven-
homeworld/extrema/chain/copy tests passed freshly too. C cost fixtures independently
reproduce 2,292 defined arithmetic cases, 22 metadata rows, seven home mappings
and 16 rounding evaluations. The approved eight-type 600-point extension is
explicitly separate from defined C behavior.

Evidence is deliberately layered. `TestCR07FActorInitialEmpires` invokes the real
actor and compares every initial field in its owned collections and its store,
plus directory/governed invariants. The real-binary journey compares every
persisted field after graceful stop and uses actual authenticated commands to
verify the subprocess actor's view. It does not expose a private model-dump route
or pretend that public status counts reveal every private field.

Both races independently dual-login through actual HTTP, validate issued JWT
signature/expiry/identity, connect over WebSocket, receive welcome and execute
`cs`, `survey`, `map`, `capital` through the real dispatcher. `cs` is silent;
the following survey verifies the resulting home scope. The supplied credentials
and stored hashes are scanned against the raw exact-schema login response;
only the intentionally issued JWT is permitted there. That JWT is forbidden in
later logs/public/CLI output. All subprocess logs and captured responses are
checked for password/hash/artifact/admin-token leakage. Earlier actual HTTP
tests freshly prove wrong credentials, governor abuse, safe legacy login and
new-race legacy bypass refusal; current JWT/WS privilege and activation revocation
are exercised by `TestEnrollmentCurrentSessionAuthority`.

## Turns, historical retry and concurrency

Both races exist before the journey's update and movement segment. Counter,
deadline and commit-sequence transitions are asserted, along with governor/home/
owned-sector/live landed-government/chain invariants after turns. Initial
efficiency/population are not incorrectly frozen across gameplay. The journey
quiesces, refuses mutation retries, gracefully stops, reads strict CR-18/43
records, restarts QUIESCED with the same schedule, resumes, replays both
historical placements with bootstrap now disabled and freshly logs in/plays
both races again. Only init/update/segment/shutdown kinds remain.

Actual gbctl/private HTTP refusals cover changed specs, different accounts
reusing IDs, second requests for one account, case-folded names, changed valid
credential versions, versions belonging to another account, invalid versions,
wrong universe and missing/invalid admin tokens. Before/after comparisons cover
the complete world, allocator ticket and all fixture placement records. Replays
before turns, after turns and after restart preserve original placement identity.
Existing actor tests freshly cover later renames/dissolution/government destruction
and prove retry skips credential preparation and search. No old bcrypt salt or
old current-world snapshot is reconstructed.

Started-timeout/status/replay evidence uses the real private HTTP handler/client
and real actor in `TestPrivateEnrollmentTimeoutAndUncertainty`, in-process;
it is not described as a killed subprocess. Buffered request-lifecycle tests
prove queued cancellation never executes and started timeouts may still commit.
New actor barriers prove two requests competing for slot 63 have one winner,
login waits behind an actual scheduled timer turn, enrollment cannot run during
that turn, a request queued before turn admission executes after that turn,
and quiesce/shutdown wait for an open enrollment transaction to finish. Existing
concurrent placement tests compete for the same first-choice homeworld/ship
ticket and freshly pass. HALTED/QUIESCED/other mode/draining/fence refusals are
covered without assuming a timeout means rollback.

## Process crashes and uncertain outcomes

The subprocess suite builds a separate `gb_enrollment_acceptance` binary inside
the existing plain Go test entry point. Its build-tag-only callback installs the
existing CR-07C hook at a closed list of stages, logs only a bounded stage marker
and waits. The parent synchronizes on that marker and SIGKILLs only its owned
disposable process. No production endpoint, unsafe debug control or workflow
change is added. `TestCR07FProductionBuildIgnoresCrashHook` proves the ordinary
binary enrolls successfully despite that test environment key.

Six process windows execute: before request, before transaction, inside the open
transaction before construction, after the race write, after all entity/free-list/
metadata writes before commit, and after successful bbolt commit before actor
installation/HTTP response. An eligible free-list entry 7 is explicitly seeded
in the closed disposable fixture. Known-aborted windows preserve every durable
bucket key/value and bucket sequence, including free lists and placement metadata;
only startup's legitimate `schedule.saved_at` rewrite is normalized. The complete
world/allocator/placement comparison also stays identical. Restart accepts a new
same-ID request with no partial empire or consumed 7. Post-commit death leaves the
whole C-derived empire and metadata with ship 7 and consumed tail; retry returns
the original result. The disconnected CLI never claims confirmed success.

Existing fresh C/D/HTTP faults separately exercise known rollback, confirmed
post-commit error/panic, simulated error despite visible writes, failed readback,
verification/install failure, uncertainty guard and halted shutdown without a
stale flush. Those do not turn visibility into durability. Restart is used in
the new process tests only for proven uncommitted callback interruption or known
successful bbolt commit. There is no automatic uncertain-store repair/reopen.
Actual disk-full/fsync/page-write failure, power loss and storage hardware
durability remain the documented bbolt/library and future operational boundary.

## CR-17/18/43 and rollback

The six direct-store planet read/write sites in load/order handlers remain.
Fresh `TestActorEnrollmentPendingCommandAPWaitsForTurn` uses a real capital
command: AP-only divergence yields ENROLLMENT_PENDING_TURN/NOT_COMMITTED, changes
no persistent records, keeps SERVING, a normal turn reconciles it and the same
request succeeds. Non-AP disagreement tests for planet/race/star/global state
and stale transaction preimages halt or refuse without overwriting evidence;
HALTED shutdown cannot flush stale actor copies. This guards enrollment; it
does not repair general CR-17 command persistence or guarantee all commands are
atomic. Conservative historical/dead-record free-list refusal can also prevent
future enrollment and needs separately reviewed reclamation policy.

CR-18 atomic turn and CR-43 maintenance suites pass freshly, including their
existing real-process crash/restart cases. Their inventory/ADR acceptance status
and outstanding live acceptance are not changed by this CR-07 scope. No new
commit kind, WorldDigest definition, schedule, maintenance schema or migration
is introduced. New metadata remains the already-merged CR-07C namespace only;
CR-07F itself has **database format impact: NONE**.

For actual-reader proof, export revisions `fc5688ced0ac998cf77f192027db90536597d3fb`
(pre-E) and `7c5b3921e5c5222b7a581d7175a7c3160ea5bbd2` (pre-CR-07), copy the
existing `go/internal/db/testdata/cr07c_legacy_reader_test.go` into each export,
and build each db test binary. Run the current integration test with each:
`CR07C_OLD_READER=/path/to/reader go -C go test ./internal/db -run
'^TestEnrollmentCR07BAndCompatibleStartup$' -count=1 -v`. Both freshly pass:
actual old startup accepts the placement key, starts QUIESCED, and preserves full
world, commit, schedule, maintenance and universe bytes. The integration test
requires the child's explicit reader-pass marker; it does not silently substitute
the current reader.

This proves **DATA FORMAT COMPATIBILITY**, not **AUTHENTICATION/SECURITY ROLLBACK
SAFETY**. Older binaries lack dual-credential/current-session safeguards and some
older targets lack durable maintenance. Any future rollback needs separate owner
authorization, closed player access, disconnected sessions, invalidated JWT
signing authority, verified recovery evidence and target-specific review. Merely
opening the store cannot authorize player exposure or resolve uncertainty.

## Security, review, labels and remaining work

The journey tests public GET/POST enrollment/options/cost and private-route
absence on the player listener with GB_ENROLL_PREVIEW=true. Existing GET `/enroll`
serves only its disabled notice; mutation routes stay refused. Strict malformed/
duplicate/null/unknown/oversized-body tests, protected-file failures, bcrypt
failures, reference binding and disabled offline `racegen -db` tests all pass
freshly. No plaintext fallback exists in the supported writer path. Metadata and
public responses contain no stored credentials. No frontend or public API was
mounted.

Independent review initially found the login-response secret-assertion gap; it
was corrected and re-reviewed without changing runtime behavior. The reviewer
independently executed the new actor suite, generated-universe journey and all
six crash windows/normal-build exclusion successfully, and checked the canonical
C fields, complete bucket/sequence evidence, barriers and scope. Final independent
acceptance review: **APPROVE WITH CONDITIONS**, recommending **CR-07F: PASS WITH
CONDITIONS** for private enrollment. All 12 review priorities, all eight Go-source
hashes, 65 matrix references, 17 new nodes and U/R counts were independently
checked against actual evidence. No blocking finding remains. The reviewer also
reran formatting/build/vet/diff checks. Conditions are hosted container/scans/SBOM/
kind CI and normal owner review; public signup, live acceptance and physical I/O
failure proof are explicitly excluded. Q1 concurrency wording was corrected to
actor/store rather than HTTP; timeout coverage remains accurately labeled real
in-process HTTP/actor. No further code change was requested.

No protected tooling, workflows, C files, golden fixtures/allowances, known-defect
inventory or lint baseline changed. Growth passes with an empty label set.
**Required acknowledgment labels: NONE for this PR.** Historical A/E/Syft labels
belong to their merged PRs and are not inherited by an unchanged tool. Owner
review and green hosted CI remain merge conditions. No merge is performed here.

Before public signup: implement separately authorized account registration and
credential recovery/rotation, identity/account ownership lifecycle, throttling/
anti-abuse controls, CR-20/session/query-token hardening, CR-17 command ownership,
both separate TypeScript applications and their accessibility/transport acceptance,
operational recovery and security-safe rollout/rollback. Go-owned OpenAPI options/
cost contracts are available but player-facing signup remains unregistered. The
legacy Go gameplay client remains single-password-only. General gameplay defects
outside enrollment remain tracked; no functioning-game or full Phase 3c verdict
is implied.

This work performed **no live deployment, image publication, Kubernetes/Argo CD/
Longhorn operation, live-volume access, live player enrollment or production
database mutation**. State changes occurred only inside temporary local test
worlds. Further operational acceptance requires separate authorization.
