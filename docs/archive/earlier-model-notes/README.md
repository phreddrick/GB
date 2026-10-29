# Earlier-model notes (historical, superseded)

These nine documents were written by the model that produced the initial Go
port (commits `339ced3` to `71eaee7`, June 2026) and lived under `go/reference/`.
They are kept for the record because they show what was claimed at the time;
**they are not accurate** and must not be used as evidence of what the port
does. The independent review of 2026-10-27 found, among other things, that the
"all commands implemented", "all tests passing" and "production ready" claims
were untrue (there were no tests at all; 45 of the 109 C commands have no Go
equivalent; the server crashed on its own admin endpoint).

Authoritative documents:

- `docs/architecture/go-code-review.md`: the current assessment, with finding ids `CR-nn`.
- `docs/architecture/microservices-migration-plan.md`: the modernization plan (revision 2.1).
- `docs/testing/known-defects.md` and `test/knowndefects.yaml`: defects tracked by tests.

| File | What it claimed | What the review found |
|---|---|---|
| `AUDIT_COMMAND.md` | command coverage audit, "56/56 player commands fully implemented" | the C dispatcher has 109 reachable commands; 45 are missing (review section 4.1); it also glosses `cew` as "cease energy weapons" |
| `AUDIT_CONSTANTS.md`, `AUDIT_CONSTANTS_SUMMARY.md` | constants verified against C | several divergences remain (CR-26, CR-27, and GD-01/GD-02 found by the golden tests) |
| `FIXES_APPLIED.md` | "no hardcoded values in critical paths" | about 70 config fields are never read; victory weights and others are hard-coded (review section 3.5) |
| `SESSION_SUMMARY.md` | victory scoring "not implemented (matches C)" | it is implemented in Go and in C (review 3.7) |
| `TEST_REPORT.md` | "Production Ready", "All Tests Passing" | no `_test.go` existed; the runs described were manual `makeuniv` invocations |
| `TESTS_MARKET_BIDDING.md`, `TESTS_PLANET_GENERATION.md` | subsystem tests | manual observations, not automated tests; market settlement is absent from the turn (CR-14) |
| `USAGE_VERIFICATION.md` | usage verified | the deployment manifest and server could not work together (CR-05, CR-06) |
