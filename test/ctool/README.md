# gb_golden: C-to-Go golden fixture generator

Phase 0 parity testing works by running the **original C functions** and
recording their outputs as CSV fixtures under `test/golden/testdata/`, which the
Go golden tests in `go/internal/game/golden_test.go` consume. The C sources
under `source/` are compiled unmodified; `main.c` provides the game globals,
`stubs.c` provides link-time stand-ins for symbols that are referenced but never
executed (any stub that is reached aborts the run with status 99), and
`include/curses.h` is an empty shim so `build.c` and `shlmisc.c` compile without
the ncurses development package.

## Requirements

`gcc` (tested with GCC 13.3; the code is 1989-era ANSI C and is compiled with
`-std=gnu89 -fcommon -w`), `make`, glibc headers.

## Commands

```sh
make -C test/ctool            # build ./gb_golden
make -C test/ctool fixtures   # regenerate test/golden/testdata/*.csv
make -C test/ctool check      # regenerate into a scratch dir and diff against the committed fixtures
make -C test/ctool clean
```

Regenerating fixtures is a reviewed change: commit the CSV diff together with
the reason (a new input grid, a new function, or a change in the C reference,
which should never happen).

## What is covered and how randomness is controlled

| Fixture | C function(s) | Determinism |
|---|---|---|
| `planet_guns.csv` | `planet_guns` (shootblast.c) | pure |
| `hit_odds.csv` | `hit_odds` | pure |
| `cew_hit_odds.csv` | `cew_hit_odds` | pure |
| `cew_hit.csv` | `CEW_hit` = `success(cew_hit_odds)` | only rows with odds 100 or 0, where the `success()` roll cannot change the result (`rng=invariant`) |
| `compatibility.csv` | `compatibility` (max.c) | pure |
| `max_support.csv` | `maxsupport` (max.c) | pure |
| `ship_design.csv` | `ship_size`, `getmass`, `cost`, `complexity` (build.c) on 47 `Getship()` default designs and 48 modified ones; see the coverage note below | pure |
| `ground_attack.csv` | `ground_attack` (move.c): `astrength`, `dstrength` only | the casualty rolls are not emitted (they need the Go RNG to be injectable, Phase 3b) |
| `ap_add.csv` | `APadd` + `governed` (doturn.c) | bracketed: each row is run twice with the RNG forced to its minimum and maximum, giving `[aps_lo, aps_hi]`; the Go result must fall inside |
| `produce.csv` | `produce` + `plate` (dosector.c) | steady-state sectors chosen so no branch consumes the RNG (`rng=none`) |

`main.c` replaces libc `random()` with a fixed-seed LCG and counts calls per row;
the `rng` column records `none` (not consulted), `invariant` (consulted but the
result cannot depend on the value), `bracket`, or `strengths-only`. Functions
whose outputs depend on the random sequence value by value (`produce` with
resources, casualties, `round_rand` consumers) are deferred to Phase 3b, when
the Go RNG becomes injectable and can be driven by the same sequence.

## What `ship_design` does and does not prove

`ship_design.csv` exercises exactly four Go functions: `shipSize`, `getMass`, `shipCostFromDesign` and `complexityOf`. The row inputs (type, primary, secondary, crew, cargo, fuel, destruct, speed, hanger, armour, devices) are the values C's `Getship()` produced, so the test does **not** exercise Go's `getshipDefaults`, and it does **not** establish parity of the whole `Shipdata` table: for the 47 types Go reads only `ABIL_COST` and `ABIL_TECH` (cost, complexity) and the eight design bases `ABIL_GUNS`, `ABIL_MAXCREW`, `ABIL_CARGO`, `ABIL_FUELCAP`, `ABIL_DESTCAP`, `ABIL_SPEED`, `ABIL_HANGER`, `ABIL_ARMOR` (complexity); the remaining columns (`BUILD`, `CONSTRUCT`, `MOD`, `LASER`, `CEW`, `CLOAK`, `GOD`, `PROGRAMMED`, `PORT`, `REPAIR`, `PAY`, `MAINTAIN`, `JUMP`, `MOUNT`, `HASSWITCH`, `DAMAGE`, `PRIMARY`, `SECONDARY`) are not compared.

## Not covered in Phase 0 (and why)

- `CEW_hit` strength multiplier at the call site (CR-27) is **not** covered by a
  generated fixture: `shoot_ship_to_ship` needs the whole server state. It is
  covered instead by `go/internal/game/cew_callsite_test.go`, which drives the
  Go call site deterministically (target at exact CEW range, radiation mode,
  no armour, body 40) and compares against the C formula at that call site,
  `hits = strength * CEW_hit(...)`. The reference there is the C source line,
  not a C execution; the harness covers `cew_hit_odds` and `CEW_hit` themselves.
- `do_damage`, `ground_attack` casualties, `produce` with resources, population
  growth: RNG value dependent.
