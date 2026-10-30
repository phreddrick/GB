# Static-analysis baseline (Phase 0, shrunk by Phase 1)

**Phase 1 update (2026-10-29).** The Phase 1 rewrite of `go/cmd/gb-server`, `go/cmd/makeuniv`, `go/internal/net`, `go/internal/game/state.go` and `turn.go` removed 73 baseline findings (the legacy code they lived in no longer exists; the universe generator moved to `go/internal/universe` with explicit bounded conversions and a seeded generator, so its 57 findings did not move with it) and added **none**: every new Phase 1 package (`admin`, `engine`, `platform/*`, `universe`, `gbctl`, the tests) is lint-clean under the same configuration, and the baseline is regenerated with `lintdiff -write-baseline` from the clean report. **Current total: 1,059** (639 errcheck, 393 G115, 5 G404, 6 S1009, 6 QF1003, 3 QF1001, 2 SA9003, 1 each SA4006, QF1011, G304, G117, G103), all in `go/internal/game` (1,032), `go/internal/db` (15), `go/cmd/gb-client` (7) and `go/cmd/racegen` (5). The shrink is a reduction, which the growth gate accepts without a label; the tables below are the original Phase 0 record and are kept for provenance.


Recorded on 2026-10-28 before any runtime source change, with `golangci-lint` v2.15.0 (`go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.15.0 run ./...` from `go/`, configuration `go/.golangci.yml`). Phase 0 does **not** repair these findings: they live in runtime code that later phases rewrite. CI compares every run with the committed baseline (`lint-baseline.txt`, format v3: `path`, `linter`, `message`, `enclosing-declaration`, `source-fingerprint`, one line per occurrence) using `go/tools/lintdiff`; **new** findings and **stale** entries both fail the build, so the debt is reported, not hidden, and cannot be reused as an allowance elsewhere.

**Provenance of the baseline.** 1,061 findings are in files that were already committed at `71eaee7`. **71 findings are in `go/cmd/*/main.go`**, the four entry points that were git-ignored before Phase 0 (CR-01): they are legacy debt from the same generated code, but they were never committed before, so they are listed here as *newly tracked legacy debt*, not as previously accepted findings. Phase 0 adds no findings of its own: its new files (`go/internal/testutil`, `go/tools/*`, the golden and call-site tests) are lint-clean under the same configuration, and the baseline is checked to contain none of them.

Total pre-existing findings: **1132**.

## By linter and rule

| Linter | Rule | Count | Burn-down phase |
|---|---|---|---|
| errcheck | errcheck | 659 | 3a (worldstore commit API returns errors; net rewrite in 4b) and 1 (server/handler paths) |
| gosec | G115 | 428 | 3a/3c (integer conversion audit when records get a single owner) |
| gosec | G404 | 21 | 3b (RNG injection replaces math/rand) |
| staticcheck | QF1003 | 6 | 3a |
| staticcheck | S1009 | 6 | 3a (store API rewrite) |
| staticcheck | QF1001 | 3 | 3a |
| gosec | G304 | 2 | 1 (path handling in config/db) |
| staticcheck | SA4006 | 2 | 3a |
| staticcheck | SA9003 | 2 | 3a |
| gosec | G103 | 1 | 3a |
| gosec | G117 | 1 | 1 |
| staticcheck | QF1011 | 1 | 3a |

## By package

| Package | Count |
|---|---|
| `go/internal/game` | 1035 |
| `go/cmd/makeuniv` | 57 |
| `go/internal/db` | 15 |
| `go/internal/net` | 11 |
| `go/cmd/gb-client` | 7 |
| `go/cmd/racegen` | 5 |
| `go/cmd/gb-server` | 2 |

## By file (top 25)

| File | Count |
|---|---|
| `go/internal/game/build.go` | 269 |
| `go/internal/game/commands_order.go` | 109 |
| `go/internal/game/commands_load.go` | 101 |
| `go/internal/game/commands_move.go` | 63 |
| `go/internal/game/planet.go` | 62 |
| `go/cmd/makeuniv/main.go` | 57 |
| `go/internal/game/commands_fire.go` | 55 |
| `go/internal/game/commands_nav.go` | 54 |
| `go/internal/game/commands_profile.go` | 53 |
| `go/internal/game/commands_power.go` | 45 |
| `go/internal/game/commands_tech.go` | 41 |
| `go/internal/game/ship.go` | 41 |
| `go/internal/game/commands_misc.go` | 35 |
| `go/internal/game/commands_teleg.go` | 24 |
| `go/internal/game/vn.go` | 23 |
| `go/internal/game/combat.go` | 20 |
| `go/internal/db/store.go` | 14 |
| `go/internal/game/commands.go` | 12 |
| `go/internal/game/helpers.go` | 9 |
| `go/internal/game/turn.go` | 9 |
| `go/cmd/gb-client/main.go` | 7 |
| `go/cmd/racegen/main.go` | 5 |
| `go/internal/net/session.go` | 5 |
| `go/internal/game/commands_cs.go` | 4 |
| `go/internal/game/rand.go` | 4 |

## How the gate works

- CI runs `test/scripts/lint-gate.sh`, which installs the pinned `golangci-lint` v2.15.0 into a private directory with `go install` and builds `tools/lintdiff` with `go build` (binaries, never `go run`, because `go run` collapses every nonzero exit into 1 and would hide the difference between findings and tool failures). It runs `golangci-lint run --output.json.path=lint.json ./...` (exit 0 or 1 are results; 3 to 7 are tool failures and abort the gate with exit 2), then `lintdiff -baseline docs/testing/lint-baseline.txt -report lint.json -prefix go/`.
- `lintdiff` first **validates** the report: it must be well-formed JSON with both `Issues` and `Report` sections, `Report.Error` empty, no failure warnings, the required linters (`errcheck`, `staticcheck`, `gosec`, `govet`) listed as enabled, and every issue carrying a linter, message and filename. Anything else is fatal (exit 2); an empty or truncated report is never read as "all fixed".
- Finding identity is `repository-relative path` + `linter` + `message` + **enclosing top-level declaration** (resolved by parsing the Go file with `go/parser`: `func (*State).doTurn`, `func main`, `var x`, or `file` for package-level lines) + **source-line fingerprint** (the offending source text with whitespace collapsed). Line numbers are used only to locate the declaration and are not part of the key, so a formatting-only move keeps its identity; the same statement in another declaration or file is a new finding (and the old one becomes stale), which closes the substitution Codex demonstrated with two identical `store.Close()` statements in different functions. Duplicates within one declaration are counted per key. A finding whose file cannot be parsed is fatal (fail closed).
- **Accepted limitation (GB0-04R, owner decision for Phase 0).** Two identical statements inside the **same** top-level declaration share one key, so an unchecked statement moved between blocks of one function, or a finding that shifts from one of two identical lines to the other, reuses the existing allowance and is not detected mechanically. Cross-function moves, duplicates (count exceeded), stale entries and ordinary new findings are detected. Consequently the gate is a useful regression detector, not a proof that no new lint debt was introduced: **changes to code that already carries baseline findings require review of the actual code diff**, and the pinned test `TestSameDeclarationSwapIsNotDistinguished` fails if this behaviour ever changes so that the documentation is updated with it.
- A key with more occurrences than the baseline is **new** (fails). A baseline key with fewer occurrences than recorded is **stale** (also fails): a resolved finding cannot leave an allowance that a later finding with the same key could consume. The baseline is then shrunk explicitly with `lintdiff -write-baseline` and reviewed in the diff. Adding to the baseline is growth and needs the `known-defect-added` approval (growth gate).
- Identification, comparison, normalisation and retirement in one place: **identify** by path, linter, message, enclosing declaration and source fingerprint; **normalise** paths to repository-relative forward-slash form (the `go/` prefix is added to golangci-lint's module-relative paths, absolute paths are made relative) and source text to single-spaced; **compare** occurrence counts per key (report versus baseline); **retire** by regenerating the baseline with `lintdiff -write-baseline` after a fix and reviewing the shrinking diff, which the growth gate accepts without approval because it is a reduction. A line-number or diff-based scheme was not used because the Phase 0 `gofmt` pass moved the lines of hundreds of old findings.
- `go vet`, `gofmt`, `go build` and `go test` are strict and were already clean (vet) or made clean (gofmt) in Phase 0.
- The baseline is burned down in the phases named above; when a phase rewrites a package, the same change removes the package's lines from `lint-baseline.txt`.
- **Two staticcheck numbers, reconciled against the actual files.** The baseline (golangci-lint v2.15.0, `checks: all` minus the ST style checks, `unused` linter not enabled) holds **20** staticcheck findings: S1009 ×6, SA4006 ×2, SA9003 ×2 and quick-fix suggestions QF1001 ×3, QF1003 ×6, QF1011 ×1. The direct `staticcheck` v0.8.2 run recorded in `phase0-baseline.md` reports **22**: U1000 ×13 (unused identifiers, which golangci-lint attributes to its separate `unused` linter, not enabled here), S1009 ×6, SA4006 ×2 and SA4017 ×1 (not reported by this golangci-lint configuration). The two sets overlap in S1009 and SA4006 only; neither is a subset of the other. U1000 and SA4017 are tracked through the direct staticcheck record and burned down in Phase 3a with the rest.
