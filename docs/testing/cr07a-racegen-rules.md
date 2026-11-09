# CR-07A: shared race-design rules and original-C fixtures

Status: implemented for review, not merged or deployed. Owner authorization:
CR-07A, including the narrow exception to Phase 3c sequencing, Q4 and Q11.
Base: `96b528dd19f47fb1cb56db1823155c1a5a3beb1a` (PR #22).

## Scope and consumers

`go/internal/racegen` owns the design specification, metadata, covariance
tables, point costs and validation. Functions use value inputs/outputs with
no mutable shared state, I/O, RNG, database or credential dependencies.
The offline CLI delegates rules to it. Its command syntax, draft editing,
database writer, player-number allocation and credential code remain in place.
Cost corrections and stricter numeric/cross-field checks are intentional.

The owner requires a separate externally accessible **TypeScript racegen and
signup application**, distinct from the TypeScript gameplay client. PR #23
records that requirement. This package supports a future Go-owned API; it
adds no route, app, identity service or external exposure. The browser is
never authoritative for costs or acceptance. Keep ADR-0001's modular monolith.

## Package contract

- `Spec`: named numeric traits, race type, homeworld, eight sector percentages,
  name, optional address and descriptive metadata. No password or privileges.
- `Default`: the original initial draft (plated-only, not yet a valid design).
- `AttributeDefinitions`, `Homeworlds`, `Sectors`, `Covariances`, `Calibration`:
  independent metadata copies, stable C indices and allowed ranges/defaults.
- `Cost`: breakdown by attribute/sector, home cost, terrain-count surcharge,
  total, remaining budget and an explicit eight-type-extension flag. It checks
  enum indices, finite numbers, numeric ranges and C integer safety. It can
  price unfinished designs, including fractional integer fields or incompatible
  terrain; a cost result is not acceptance. Errors return no partial breakdown.
- `Validate` / `Validated`: full non-mutating design validation and field/code
  errors; the latter returns an unchanged spec only on success. Includes
  booleans, integral traits/sector percentages, normal-only ability exclusions,
  gas/plated rules, minimum terrain count, 100% common terrain and budget.

Fertilize/adventurism use fractions; sector compatibility uses percentages.
These design values must be deliberately converted to world model fields in
later work. A validated species is not an authorization to enroll.

This is not a complete wire decoder. A future authenticated API must reject
unknown fields, missing/null required numbers, wrong primitive types, and
sector arrays of the wrong length before constructing `Spec`. Standard Go
JSON decoding accepts null numbers as zero and discards excess fixed-array
elements; validation cannot recover that lost information. Do not expose this
package by directly decoding an untrusted body and assuming its tags enforce
the complete request contract.

## Original-C fixture provenance

Behavioral authority: unmodified `source/racegen.c`, `racegen.h`, `game_info.h`
and the `planet_translate` table in `source/GB_racegen.c`, at the base above.
No C++ code or cost constant supplies expected results.

`test/ctool/racegen.c` includes the original C translation unit with only its
interactive `main` renamed. The separate original `GB_racegen.c` object
supplies the mapping table. Section garbage collection removes unused
interactive and world-writing functions; none of them is invoked or replaced
by a successful stub. No curses dependency is needed for this generator.

Before each row, the harness restores pristine C attribute parameters, supplies
the documented initial race, calls `normal`, calls original `cost_of_race`,
subtracts its rounded attribute costs from `l_fudge`, then applies `metamorph`
when requested. This is `initialize`'s integer calibration (lines 684–687),
excluding its inverted `memcpy(&race, &last, ...)` which overwrites the intended
default input. It never calls the broken interactive initializer.

| Fixture | Evidence |
|---|---|
| `racegen_cost.csv` | 2,292 defined-C cases; every attribute/sector rounded cost, home cost, surcharge, total and remaining points |
| `racegen_metadata.csv` | 22 rows: normal/metamorph trait ranges, defaults, numeric kinds, calibration and every genetic covariance |
| `racegen_homeworld.csv` | All seven names, mappings, home costs and planet/sector covariance multipliers |
| `racegen_round.csv` | 16 original `ROUND` evaluations around positive/negative half boundaries |

The cost grid includes all trait minima/defaults/maxima for both race types,
every trait pair at four extrema, all home/sector pairs, every sector pair,
all 128 non-gas terrain masks on each home, and 512 fixed-LCG mixed designs.
Some rows deliberately violate design acceptance rules while their arithmetic
remains defined. The C harness independently establishes **175 spent / 1,225
remaining** for the initial Earth draft; production Go code has no such constant.

Regenerate with `make -C test/ctool fixtures`; verify with
`make -C test/ctool check`. Existing fixture bytes must remain unchanged.
The Go tests consume committed CSVs and need no C compiler at test time.
The generator was also run with GCC undefined-behavior and float-cast-overflow
sanitizers; generated cost fixtures matched exactly without sanitizer failures.

Source SHA-256 values:

| File | SHA-256 |
|---|---|
| `source/racegen.c` | `90dd7a4e220e62297a5ec47a7fa7712549a0bb2c84b4fa0b7cad57ded16c29bf` |
| `source/racegen.h` | `c828722667c73c49f59610d5428b771199e53b4b2c7f86ea66b577c4f4770f91` |
| `source/GB_racegen.c` | `4926f34a1f37d23f941ce28630ba758f34718ebd1bd92f7bc8f153d62220650b` |
| `source/game_info.h` | `1405b919969039d88ee34fafbd47fb9e5ceea8b5f6a61f0e59418ddb169d8e15` |

## Explicit compatibility decisions and departures

1. Preserve C's `int(0.5+x)` truncation, integer normal-default calibration,
   multiplication order and retained BIRTH/FIGHT/METAB × IQ metamorph terms.
2. Owner-approved extension: eight nonzero sector types cost **600** points.
   Historical C fixtures never read `blah[8]`. A separately named Go extension
   test prices a bounded fractional draft and distinguishes it from the C
   seven-type surcharge. Full validation still rejects fractional percentages
   and incompatible gas/non-gas combinations.
3. Reject non-finite values, invalid enum indices, out-of-range numbers and
   arithmetic outside defined signed 32-bit C integer operations. Gas covariance
   can overflow even with individually in-range inputs; return `COST_OVERFLOW`.
4. Validation rejects fractional integral fields instead of C's in-place
   truncation. Decimal traits are evaluated as supplied, without silent
   hundredth quantization. C's omitted water checks are restored; a common
   100% terrain is required. No invented metamorph-one-sex constraint is added.
5. Address is optional metadata (owner Q11); credentials/privileges are outside
   pure species rules. The CLI retains its existing local password checks.
6. Airless metadata maps to Mars as C specifies. The CLI's existing world-record
   builder, including its incorrect LikesBest mapping/scaling, is not repaired
   by this unit; WU-B/D must perform those conversions correctly.

## Validation and review

Local results and limitations are recorded in the implementation PR, including
Go build/vet/tests/race, C reproducibility, sanitizer checks, lint and inventory,
growth gating, offline manifests and workflow policy. Container builds, image
scans and kind integration belong to GitHub-hosted Actions only.

Local acceptance: build/vet, full tests and inventory reconciliation, full race
suite, C fixture reproduction, focused fuzzing (139,224 executions), lint
(1,057 baseline findings matched; zero new/stale), offline manifest suite
(21 schema-valid resources), Python workflow helpers and workflow policy passed.
The full tests initially failed temporary-module VCS discovery because this
environment has an invalid read-only `/tmp/.git` marker. Rerunning with `TMPDIR`
in this worktree's ignored `test/ctool/obj/tmp` passed without modifying tests or
disabling VCS checks. Disposable listener tests needed sandbox socket permission.

An independent agent reviewed the rules, CLI, harness and tests: APPROVE for
CR-07A, subject to owner acknowledgment and hosted CI; no blocking findings.
Its requested CLI regression tests and explicit future-decoder limitations were
added and re-reviewed. It independently ran targeted race/vet tests, fixture
reproduction and sanitized C generation.

Protected tooling changed: `test/ctool/Makefile`, its ignore file and the new
fixture driver. **The owner must apply `gate-change-reviewed` to the PR.** Local
label simulation is not acknowledgment. No known-defect allowance, expected
divergence or lint baseline changes are intended, so `known-defect-added` is
not required.

Independent review is required before recommending merge. This work neither
authorizes deployment nor closes CR-07 as a whole.

## Outstanding for WU-B and later units

Owner decisions remain for race-number/God policy, temperature/vacancy,
placement RNG, capital fallback, governor credentials and ship-number reuse.
WU-B should consume this validated design and metadata, inject deterministic
star/sector order and create isolated world copies; preserve existing ship
chains and test every initial C field. No placement implementation is here.

WU-C/D must correct the audit's retry ordering, caller/spec key binding,
post-commit panic handling and durability-uncertain outcomes. Test placement
metadata explicitly (WorldDigest does not cover arbitrary meta keys), prove
old-reader compatibility without new CR-18 kinds, and test subsequent CR-17
command/turn/restart behavior.

Existing **plaintext-password fallback** and **inactive-governor authentication**
defects remain unfixed and block external enrollment. Identity, permissions,
abuse controls, authenticated API, standalone TypeScript frontend, public
signup and live operations require separate implementation/review approvals.
