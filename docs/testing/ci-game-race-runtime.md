# CI game race-suite runtime (#76)

Baseline: `5ac223db8fa4127267f5338cafd425bb491f1d5c` (PR #75).
This is a test-fixture change, not a gameplay, storage-format or CI-policy change.
Issues #41, #38 and #51 remain open. #41A is merged through PR #75 but has not
completed post-merge acceptance. This runtime fix requires its own authorized
merge and successful master CI before #41A can be recorded as accepted.

## Diagnosis and preserved baseline

The actual hosted logs were read and preserved before changing fixtures:

| Run / job | Normal game package | Race game package | Outcome |
| --- | ---: | ---: | --- |
| PR #75, 43174059176 / 131033917898 | 43.040 s | 419.166 s | All seven jobs passed |
| Master 43177035240 attempt 1 / 131043680806 | 57.819 s | 600.073 s | Default ten-minute package alarm |
| Same run, attempt 2 / 131274014130 | 60.667 s | 600.064 s | Same alarm |

The timeout stacks show several population groups only 4–15 seconds old,
individual scenarios 0–1 seconds old, and runnable JSON decoding/enrollment
setup. Some groups were still waiting for the Go parallel-test semaphore.
These are cumulative package-budget failures, rather than a single population
test spending ten minutes blocked. No independent assertion failure or race was
reported before either alarm. This does not establish correctness by absence of
a report: fresh full-suite execution and the existing failure assertions remain
required.

Fresh local baseline, unchanged sources: game race package **502.025 s**,
1,297 passing nodes / 223 top-level tests. The profiled command also included
`-json`, CPU and allocation profiling; it is not presented as an exact replica
of the unprofiled hosted command. A short instrumentation-overlay run overlapped
part of this measurement and is recorded separately in the ledger.

CPU profile: 500.39 s sampled interval, 655.66 sampled CPU-seconds across
goroutines. `EnrollTrusted` accounts for 151.15 cumulative sampled CPU-seconds
(23.05%); JSON unmarshalling 165.00 (25.17%). These inclusive costs overlap and
must not be summed. Race-detector instrumentation accounts for substantial CPU.
Representative fixture instrumentation reports:

| Phase | Representative race-enabled elapsed time |
| --- | ---: |
| Generate and normalize four-star/eight-planet universe | 0.46–0.88 ms |
| PutUniverse | 21.7–26.0 ms |
| Open actor | 42.8–49.5 ms |
| Actual first-race enrollment | 298–317 ms |
| Credential hashes | 1.33 s once; subsequent calls 5–8 µs |

Universe generation and repeatedly generating bcrypt hashes are therefore not
the dominant repeated fixture costs. Hashes were already cached at the original
cost. Each command case previously repeated real enrollment, including full
world/preimage JSON validation, although enrollment is a prerequisite rather
than the command behavior under test. The earlier serial command families also
consume most of the budget before the parallel population groups start. Runner
variation explains why an already marginal package sometimes passes.

## Bounded correction and isolation contract

`command_fixture_test.go` builds one pristine prerequisite through the existing
`newEnrollmentActor`, `PutUniverse` and actual `EnrollTrusted` workflow. It saves
only immutable database bytes, value-only directories, placement identity and
policy callbacks closing over immutable test credential strings. The factory,
habitat and exploration-dump fixture constructors consume this prerequisite;
cargo, population and telescope fixtures inherit it through the factory fixture.
All real enrollment/bootstrap/authentication acceptance fixtures remain on the
original independent initialization/enrollment path. Second empires required by
command tests are still enrolled through the real actor.

The seed file is read synchronously inside its exclusively owned actor's test
callback, after confirmed enrollment. No hook, external writer or other test
receives that seed Store. The actor cannot execute a scheduled turn or shutdown
while this callback runs, and bbolt has no autonomous writer. This narrowly
controlled disposable fixture capture is not a general/live-store backup API.
The capture precedes cleanup, so it adds no shutdown commit to the prerequisite.

Every consumer writes a private 0600 file from a fresh byte conversion and opens
its own Store, State, config, policy object, registry, log, hooks and model graph.
It receives neither an mmap nor mutable models from the seed. Startup retains
normal strict metadata validation. Each copy receives its own fresh schedule
using the existing `freshSchedule`/`scheduleRecord` functions, so cached deadlines
cannot age into automatic turns. The three schedule timestamps are the only
per-copy persisted differences. Actor-local initial home directories are copied
by value, preserving the original immediately enrolled command scope.

`TestCommandFixtureMatchesRealEnrollment` compares the complete actor world and
directories with an independently initialized, genuinely enrolled actor. It
also compares every logical bbolt bucket, key, value and bucket sequence with
the original seed. Only the three fresh schedule timestamps are normalized;
turn counters, intervals, commit metadata, placement metadata, auxiliary records
and allocator sequences remain exact. Placement identity is independently
compared, excluding its timestamp and corresponding record checksum.

`TestCommandFixtureIsolation` exercises eight parallel cases. Each mutates its
own ships, star, embedded sectors/Planet.Info, race governor, universe AP,
directory, hooks, policy, config and write fence, then verifies another existing
copy and a later copy remain identical to the seed. No process environment or
shared mutable fixture model is introduced. Existing command tests keep every
assertion, real actor dispatch, turn/restart boundary, fault hook and SIGKILL.
No existing test scheduling is changed.

## Review and execution evidence

Fresh local measurements (Linux amd64, 16 logical CPUs; no concurrent heavy
validation during the three post-change whole-suite measurements):

| Execution | Game package | Whole command | Test result |
| --- | ---: | ---: | --- |
| Unmodified, profiled game race baseline | 502.025 s | 508.526 s | 1,297 game nodes |
| Exact `go test -count=1 -race ./...` | 321.246 s | 330.091 s | PASS |
| Same exact command, empty private Go build cache | 327.055 s | 353.887 s | PASS |
| `GB_LONG_TESTS=1 go test -count=1 -race -json ./...` | 310.753 s | 317.103 s | 4,605 nodes / 464 top-level; no test skips |
| Final `go test -count=1 -json ./...` | 31.692 s | 32.710 s | 4,603 nodes / 462 top-level; two existing opt-in skips |
| Population + fixture checks, `GOMAXPROCS=4`, race, `-count=3` | 63.831 s | See ledger | 669 nodes / 45 top-level; 72 SIGKILL leaves |

The first exact race run preceded the added bucket-sequence assertion in the
new verification test; both the cold-cache and long-suite runs include it.
The repeat measurements are 35–38% below the profiled local baseline and have
45–48% margin below 600 seconds. The cold run clears the Go build cache, not OS
page caches or the module cache. `GOMAXPROCS=4` bounds Go scheduling; it does not
turn this host into a dedicated four-CPU hosted runner. These results support
repeatability locally, not a guarantee of hosted reliability. Hosted timings
and all seven checks must also be reviewed; post-merge master acceptance remains
a separate owner-controlled step.

Full long execution retains **all 1,297 original game test identities**, adding
only ten fixture-verification nodes. All 345 population nodes across rules,
storage and actor tests pass, including the original **24 real SIGKILL** leaves.
The four population actor test files and every existing crash harness are
unchanged. No command assertion or real-process boundary was removed.

A temporary, uncommitted Go source overlay bypassed `handlePopulation`, restoring
the actual legacy writer with the new prerequisite fixture. All eight original
ship/planet × crew/troop × load/unload atomicity leaves failed for partial
population/actor-store disagreement; both ownership-accounting edge cases also
failed. The expected exit was 1, with ten failed leaves, not a build failure.
The raw failures and overlay are preserved. This negative control demonstrates
that fixture reuse has not hidden the original defects.

The validation ledger records commands, exit status, actual durations, test
counts, source hashes and raw-output SHA-256 hashes. Historical failed hosted
attempts remain evidence, not overwritten by a later pass. All tests use
disposable stores/processes with Go 1.27.4 and `GOTOOLCHAIN=local`.

Independent read-only review identified a missing allocator-sequence comparison
in the new fixture-equivalence test. It was added; the image copy itself already
preserved those counters. Full review history and final validation results are
recorded alongside the ledger.

The separate reviewer approved the final source and local evidence after the
correction, including its own bounded, noninteractive Go 1.27.4/local race run:
`GOMAXPROCS=4`, `-count=2`, `-run '^TestCommandFixture'`, 20 passing nodes / four
top-level executions in 6.698 seconds. Source hashes matched before and after.
No code blocker remains. This approval does not stand in for hosted CI or
maintainer merge authorization.

All applicable local gates passed unchanged: build, formatting, vet, C fixture
reproduction and golden parity, inventory reconciliation, all 15 golden
mutations, lint (1,000 matched / zero new / zero stale), growth, all three gate
self-test scripts, offline manifests (21 valid resources), workflow policy (17
checks), workflow helper tests (188), actionlint, shellcheck, strict govulncheck,
module verification/tidy and diff checks. Govulncheck reports zero affected
vulnerabilities and 17 uncalled module advisories; no advisory is suppressed.

Evidence: [validation ledger](evidence/ci-game-race-runtime-validation.json) and
[lossless raw logs, profiles, overlays and review history](evidence/ci-game-race-runtime-logs.tar.gz).
The ledger binds execution outputs and the four changed test sources by SHA-256.
The earlier preliminary executions and deliberate negative-control failure are
retained separately from final-source acceptance. A local `go tool pprof` probe
failed because that optional executable was absent; the successful analysis ran
the pinned toolchain's bundled `cmd/pprof` sources instead.

## Boundaries

No production code, original C, gameplay rules, credentials, database format,
commit kinds, lint baseline, defect allowance, workflow or protected gate script
changes. The required `go test -count=1 -race ./...` and default 600-second package
timeout remain unchanged, as do all seven hosted jobs. All 24 population real
SIGKILL cases and the other command/enrollment/turn/maintenance regressions remain
required. Atomic operations still do not imply command deduplication or safe
blind retries; this change makes no new CR-17 durability claim.

Hosted containers, Syft, Trivy and kind remain GitHub Actions responsibilities.
No merge, deployment, image publication, live database or Kubernetes operation
is part of this work. No maintainer acknowledgment label is expected because no
protected tooling or allowance changes are included; the unchanged growth gate
must confirm that result.

## PR #88 follow-up — bound the game race processes

PR #88 and its master merge have the same Git tree,
`8b5a6e312c6672cc52a1a1b013c88fb2fb75fa6c`. The successful PR run took
37.433 s for the normal game suite and 365.764 s under race. The master run
took 54.710 s normally and reached the 600 s game-package alarm under race
(600.072 s). The active ground-assault crash group had run for 12 s and its
current subtest for less than a second. The log contains no earlier assertion
failure or data-race report. These runs establish a cumulative runtime
problem; they do not identify the cause of the runner-to-runner variation.
[Successful PR job](https://github.com/phreddrick/GB/actions/runs/44876577062/job/136762484806)
and [master timeout](https://github.com/phreddrick/GB/actions/runs/44880103074/job/136773297947).

Run `test/scripts/race-check.sh` to reproduce the CI race step. Go discovers
all packages with race build constraints. Packages other than `internal/game`
run once without a filter. The driver runs game twice, selecting the CR17,
cargo and planet-factory test families with `-run` and their
complement with `-skip` using the same anchored top-level expression. Each
family keeps its full subtest tree and existing parallel/serial scheduling.
New test names enter exactly one group. Each invocation uses `-count=1 -race`
and Go's existing default timeout. All three invocations run even if an
earlier one fails, and any failure fails the required build/test/lint job.

Game fixtures use disposable stores and actors. The immutable enrolled-world
seed initializes once per test process. Real crash children are launched by
their parent test with their own boundary and database. Tests that share
inventory accounting in process (`TestGolden` and `TestCEWCallSite`) run
together in the complement, which has no `-run` filter. Its TestMain still
validates the inventory. The normal suite and record reconciliation stay
unfiltered. The driver preserves `GB_LONG_TESTS` from the caller.

The groups run sequentially on the same runner. This preserves all seven
hosted job names, their permission boundaries and the existing CI provenance
contract. The objective is headroom within each package deadline. Total
workflow duration can vary with runner speed and process startup.

The first local partition also selected the stockpile and walk families. Its
race processes took 271.349 s and 137.419 s. Stockpile and walk contributed
65.050 s of serial top-level tests to the longer process; the final expression
leaves them in the complement. Compare `(package, full test name, outcome)`
from `go test -json` events when changing the partition. Counts alone cannot
detect one omitted subtest replaced by a duplicate.

A disposable two-package Go module exercised failure propagation through the
driver. Each of the three invocations was made to fail in turn, in a real
race-enabled nested subtest. In every case the driver exited 1, the other
invocations still ran, and each expected test and subtest ran exactly once.
With the failure disabled the same fixture exited 0.
