# ADR-0002: C-to-Go golden-test method for parity

Status: Accepted 2026-10-28 (implemented in Phase 0: `test/ctool`, `test/golden/testdata`, `go/internal/game/golden_test.go`)

## Context

The C sources under `source/` are the behavioural reference, but there is no running C game to compare against, and the Go port's own notes about parity proved unreliable. The formulas that decide game outcomes (combat odds, production, costs, compatibility, action points) are small pure functions in both languages. Parity has to be established mechanically before any rule is "fixed" in Go, or the fixes cannot be told apart from new mistakes.

## Decision

1. A small C program, `test/ctool/gb_golden`, links the **unmodified** reference translation units (`shootblast.c`, `build.c`, `max.c`, `dosector.c`, `doturn.c`, `move.c`, `shlmisc.c`, `misc.c`, `rand.c`) with a `main.c` that owns the game globals and a generated `stubs.c` for symbols that are referenced but never executed. Any stub that is reached aborts the run, so fixtures can never depend on stubbed behaviour. The C tree is never edited; an empty `include/curses.h` shim satisfies two `#include <curses.h>` lines.
2. The harness replaces libc `random()` with a fixed-seed generator and records per row whether the RNG was consulted (`rng` column: `none`, `invariant`, `bracket`, `strengths-only`). Deterministic functions are compared exactly (integers) or within a relative tolerance of 1e-9 (floating point, to absorb libm versus Go `math` last-bit differences). Functions that round stochastically are bracketed by running the C code twice with the RNG forced to its minimum and maximum, and the Go result must fall inside. Value-by-value comparison of RNG-dependent paths waits for Phase 3b, when the Go RNG becomes injectable and can be driven by the same sequence.
3. Fixtures are CSV files committed under `test/golden/testdata/`. Regeneration (`make -C test/ctool fixtures`) is a reviewed change; CI runs `make -C test/ctool check`, which regenerates into a scratch directory and fails if the committed fixtures differ.
4. Go golden tests live in the package that owns the functions (`internal/game`, because the functions are unexported) and report through the known-defect inventory (ADR-0003), one result per fixture column, so a divergence is attributed to a finding id.

## Alternatives considered

- Re-implementing the C formulas in the test as "expected values": that tests Go against the test author's reading of C, not against C; rejected.
- Running the whole C server and comparing sessions: no C data files exist, the C server is interactive and single-process, and most divergences are in formulas; deferred to turn-level fixtures in Phase 3b.
- Transpiling C to Go for testing: heavy tooling for 1989-era code with no benefit over compiling it.

## Consequences

- Needs a C compiler and `make` for fixture generation (GitHub's `ubuntu-latest` has them; the owner installed `gcc` locally for Phase 0). The Go tests themselves need only the committed CSV files.
- Coverage in Phase 0 is the deterministic subset: `planet_guns`, `hit_odds`, `cew_hit_odds`, `CEW_hit` at certain odds, `compatibility`, `maxsupport`, `ship_size`, `getmass`, `cost`, `complexity` (which also validates the `Shipdata` table), `ground_attack` strengths, `APadd` (bracketed), steady-state `produce`. The method found two divergences the review had not listed (GD-01, GD-02).
- Not covered yet and recorded as such: the CEW strength multiplier at its call site (CR-27), casualties, resource production, population growth (`test/ctool/README.md`).

## Validation

- `make -C test/ctool check` passes in CI on every change.
- `go test ./internal/game -run TestGolden` prints the PASS/XFAIL/XPASS/FAIL summary; the inventory lists every XFAIL with its finding.

## References

- `docs/architecture/microservices-migration-plan.md` sections 16.4 (Phase 0), 17.1, 21.
- `docs/architecture/go-code-review.md` section 9 (tests that must exist before refactoring), section 4 (matching and divergent formulas).
- `test/ctool/README.md`.
