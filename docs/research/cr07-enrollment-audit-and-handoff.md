# CR-07: player enrollment and initial colony creation — three-way audit and implementation handoff

**Status:** research and plan, **not an implementation authorization**. Read-only audit performed 2026-11-09 UTC; this file is the only artifact it produced (owner-authorised exception). No source, test, manifest, inventory or existing document was changed.

**Revisions audited (verified by `git rev-parse` / `git log` on 2026-11-09):**

| Tree | Revision | Notes |
|---|---|---|
| `phreddrick/GB` `master` | `7c5b3921e5c5222b7a581d7175a7c3160ea5bbd2` (merge of PR #21) | matches the brief; the audit read a `git archive` export of this commit, not a working tree |
| original C (`source/`) | same commit | behavioural authority |
| `kaladron/galactic-bloodshed` `main` | `b67e4c3b5c1b87bf3925a001f0e17a9fc8db8fd4` | equals the previously inspected revision; secondary reference only (Apache-2.0; do not copy code) |

All line numbers below refer to those revisions. `C/` = `source/`, `K/` = the C++ repository root, `go/` = `go/`.

**Labels used throughout:** **[V]** verified by reading the cited code; **[R]** recommendation; **[A]** assumption or open point that must be checked before relying on it.

---

## 1. Executive summary and current CR-07 status

**[V] CR-07 is confirmed open on `master`.** Both Go enrollment paths write exactly one record, the `model.Race`, and nothing else: the web/actor path at `go/internal/game/admin.go:348-403` (`State.Enroll` → `enroll.Enroll` → `store.PutRace`) and the offline CLI at `go/cmd/racegen/main.go:617-706` (`buildModelRace` → `store.PutRace`). No home planet is searched, no sector is owned, no government ship exists (`Race.GovShip` stays 0), governor 0 has no home or default scope, the star is not marked explored or inhabited, and no AP is granted. In the engine this means `governed()` (`go/internal/game/turn.go:660-666`) is false forever, tax and tech production are gated off (`planet.go:437,456`), and `cs` without arguments lands at universe level, star 0 (`commands_cs.go:31-46`). The code itself says so: `EnrollPreviewNotice` at `admin.go:342`, `engine.ErrEnrollmentDisabled` at `engine/engine.go:210-213`, and the operator guide `docs/operations/local-server.md:195-207`. The endpoint is disabled by default (`GB_ENROLL_PREVIEW=false`, Decision 9) and the development server runs with it off.

**[V] What the original C does** (section 2): for player `Numraces()+1` (refused at 64, so at most 63 races) it maps the race's home-planet type to a planet type, searches uninhabited stars with at least two planets for a vacant planet of that type within a temperature window, sets governor 0's home and default scope, copies the planet's conditions into the race, sets the translation table, picks a capital sector of the race's best-liked type on the planet's sector map, owns and populates it (`popn = number_sexes`, `fert 100`, `eff 10`), builds one `OTYPE_GOV` ship landed on that sector with crew 10 and tech 100, links it as the planet's orbit list head and as `Race->Gov_ship`, marks the planet info (`numsectsowned 1`, `explored 1`) and `maxpopn`, marks the star explored and inhabited by the player and gives it 5 AP, and writes ship, race, sector, planet, star in that order with no transaction.

**[V] The C++ reference** (section 3) reproduces that placement faithfully in one service, adds 11 archetypes, planet-default sector compatibilities and a session-based designer, but has no transaction around its seven or more writes, no player cap, several formula deviations from C (rounding, metamorph covariances, temperature window dropped) and unguarded bypass fields. It is useful for test patterns and the API shape, not as a specification.

**[R] The plan** (sections 5 to 9): keep the modular monolith (ADR-0001), run enrollment as the existing typed actor request, compute everything in memory with pure placement functions, and persist race, planet (with its sectors), star, government ship, the ship-number allocation and an idempotency record **in one bbolt transaction** through a new narrowly scoped store method, without widening the CR-18 commit-record kinds (that would make the store unreadable by the currently deployed image) and without waiting for CR-17. Seven work units in dependency order, each small, each with world-state tests against a seeded disposable universe and C-derived fixtures. Nothing is implemented here.

**Not in scope of CR-07** (per the migration plan Phase 3c, `docs/architecture/microservices-migration-plan.md:1402-1416`): the identity service and account model (Phase 6/6m), the typed browser API (Phase 4b/4c), the TypeScript client (Phase 7), archetypes (optional, later), market/turn corrections (CR-10 to CR-16).

---

## 2. Original C enrollment behaviour (behavioural authority)

### 2.1 Which C code is live **[V]**

Two programs enrol (`C/Makefile:213`):

| Binary | Objects | Role |
|---|---|---|
| `enrol` | `C/Makefile:189-196` (enrol.o files_shl.o files_rw.o files.o max.o perm.o rand.o shlmisc.o) | interactive deity tool; no point budget; `C/enrol.c:80-478` |
| `racegen` | `C/Makefile:199-208` (enroll.o racegen.o files_shl.o files_rw.o files.o max.o perm.o rand.o GB_racegen.o shlmisc.o); only `racegen.o` is compiled with `-DPRIV` (`C/Makefile:250-251`) | Leonard Dickens' point-buy designer plus deity enrollment; the data-file writer is `enroll_valid_race()` in **`C/GB_racegen.c:66-373`** |

**`C/enroll_valid.c` is not built** (absent from the Makefile) and cannot be: it uses `sect->des` (`enroll_valid.c:241,246`), a field `struct sector` does not have (`C/vars.h:120-138`). Treat it as an earlier draft of `GB_racegen.c`. The two differ in that the draft forces the capital sector's type to `likesbest` and still honours a `STATUS_ENROLLED` guard; the live file does neither (`GB_racegen.c:74-79` commented out). The 2026-10-27 code review and the C++ audit below cite `enroll_valid.c` line numbers; the live equivalents are in `GB_racegen.c` and are cited here.

Other facts: `ENROLL` is never defined, so `init_enroll()` (`GB_racegen.c:58-61`, `srandom(getpid())`) is never called and racegen's star permutation uses an unseeded `random()` (`C/racegen.c:688-690`, `C/racegen.h:31-35`); `enrol` does seed (`C/enrol.c:99`). The record format racegen writes and `process` reads is `print_to_file`/`load_from_file` (`C/racegen.c:1163-1250`, `898-945`): whitespace tokens between `<************` and `************>` (`C/racegen.h:48-49`).

### 2.2 The fourteen behaviours **[V]**

1. **Player number.** `Numraces()` = race-file size / `sizeof(racetype)` (`C/files_shl.c:390-396`). `Playernum = Numraces()+1`; refused when `>= MAXPLAYERS` (64, `C/vars.h:26`): `enrol.c:101-104` exits, `GB_racegen.c:88-94` sets `STATUS_UNENROLLABLE`. Numbers are never reused (`putrace` writes at offset `(Playernum-1)*sizeof`, `files_shl.c:350-353`; `dissolved` is never consulted). racegen additionally requires the **first race to be God** (`GB_racegen.c:82-87`). The server loads races only at startup or on deity `@@reset` (`C/GB_server.c:355`, `1588-1594`, `1650-1659`).
2. **Attributes, costs, budget.** Eleven attributes in order ADVENT, ABSORB, BIRTH, COL_IQ, FERT, A_IQ, FIGHT, PODS, MASS, SEXES, METAB (`C/racegen.h:57-70`; table `C/racegen.c:192-249`, columns `e_factor, e_fudge, e_hinge, l_factor, l_fudge, minimum, init, maximum, cov[11], is_integral`). `cost_of_race()` `C/racegen.c:325-365`: per attribute `exp(e_fudge*(v-e_hinge))*e_factor + v*l_factor`, times `(1+cov[j]*v_j)` for each non-zero covariance, plus `l_fudge`, rounded with `ROUND(f)=(int)(0.5+f)` (`:330`, truncation toward zero: `ROUND(-350.5) = -350`); `l_fudge` is calibrated once so each `init` value costs 0 (`:684-687`, from already rounded costs, so it is an integer); plus `planet_cost[home]` {Earth 75, Forest 50, Desert 50, Water 50, Airless -25, Iceball -25, Jovian 600} (`:254-256`); plus per sector type `c*0.5 + 10.8*log(1+c)` (`:349`) with pairwise `compat_cov` (`:274-285`, 999 for gas with anything) and `planet_compat_cov[home][i]` multiplier when `> 1.01` (`:287-304`, `:358-360`); plus `blah[n_sector_types]` {-1,0,50,100,200,300,400,500} (`:270-272`; **index 8 is out of bounds in C** when all eight types are liked). Covariances: `normal()` `:397-421`; `metamorph()` `:368-395` mutates the same table in place and **does not reset** `BIRTH/FIGHT/METAB × A_IQ` set by `normal()` (which `initialize()` always calls first, `:682`), so C metamorphs keep those three IQ covariances. Budget `STARTING_POINTS` 1400 (`C/game_info.h:26`); `npoints = 1400 - cost` (`C/racegen.c:1514-1515`, `C/enroll.c:206-207`). Validation `critique_to_file` `C/racegen.c:443-641` (ranges, booleans, truncation **that mutates the race**, normal races no absorb/pods, name, privilege, home type, plated 100 for non-Jovian, gas rules; loop at `:554` starts at `S_WATER+1` so **water compat is never range-checked**; rigorous: password ≥ 4 and not `XXXX`, address not `Unknown`, points ≥ 0, ≥ 2 sector types for non-Jovian, 100% compat with a sector type whose `planet_compat_cov == 1.0`). Server side, `enroll_player_race` (`C/enroll.c:24-134`) runs the rigorous critique but lets the deity "enroll anyway" (`:45-46`); `enroll_valid_race` re-checks only God-first, the cap and a free planet.
3. **Homeworld search.** `ppref = planet_translate[home] = {0,6,7,5,2,3,4}` (`GB_racegen.c:30`): Earth→`TYPE_EARTH` 0, Forest→6, Desert→7, Water→5, **Airless→`TYPE_MARS` 2**, Iceball→3, Jovian→`TYPE_GASGIANT` 4 (`C/tweakables.h:241-248`; asteroids are never homes). racegen draws stars **without replacement** from a permutation (`GB_racegen.c:107-115,134`), skips a star with `numplanets < 2` or any `inhabited` bit (`:118-120`), scans planets in index order and takes the first with `type == ppref && RTEMP ∈ [-200, 100]` (`:123-131`); exhaustion → "Didn't find any free %s; choose another home planet type." (`:139-145`). `enrol` (`C/enrol.c:127-219`) starts at star 0 then draws **with replacement** up to 100 times, requires `RTEMP ∈ [-50, 50]` and additionally requires the planet to be **vacant**: no `planet->info[i].numsectsowned` for any player (`:181-184`); it falls back through the other planet types and gives up only when all are exhausted (`:205-217`).
4. **Occupied worlds.** Both skip stars with any `inhabited` bit (`enrol.c:170-171`; `GB_racegen.c:118-119`); only `enrol` checks planet-level vacancy. `explored` flags are never consulted. The `inhabited` bits are re-derived every update from `numsectsowned`/population (`C/doturn.c:291-294, 315-319`).
5. **Star and planet assignment.** Race: `governor[0].homelevel = deflevel = LEVEL_PLAN` (2), `homesystem = defsystem = star`, `homeplanetnum = defplanetnum = pnum` (`enrol.c:235-237`; `GB_racegen.c:159-161`); `conditions[RTEMP..OTHER]` copied from the planet (`enrol.c:254-255`; `GB_racegen.c:168-169`; TOXIC not copied). Planet: `info[P-1].numsectsowned = 1`, `planet->explored = 0`, `info[P-1].explored = 1` (`enrol.c:445-447`; `GB_racegen.c:334-336`); `planet->ships = shipno` (`enrol.c:373`; `GB_racegen.c:268`). Star (re-read first): `setbit(explored, P)`, `setbit(inhabited, P)`, `AP[P-1] = 5` (`enrol.c:464-467`; `GB_racegen.c:360-363`); `Sdata.AP` untouched.
6. **Capital sector.** racegen (`GB_racegen.c:225-244`): `likes[i] = compat[i]/100`; `likesbest` = the **last** sector type with `compat == 100 && planet_compat_cov[home][i] == 1.0` (0 = SEA if none); random permutation of the whole map (`PermuteSects`, `C/perm.c:23-41`); first sector whose `condition == likesbest`, else **(0,0) regardless of type**; sector type and condition unchanged; `owner = race = P`. `enrol` lets the deity choose a present type from a census (`enrol.c:304-345`).
7. **Initial population and conditions.** `sect->popn = planet->popn = number_sexes`, `fert = 100`, `eff = 10`, `troops = 0` (`enrol.c:452-455`; `GB_racegen.c:245-248`); `planet->maxpopn = maxsupport(Race, sect, 100.0, 0) * Maxx * Maxy / 2` (`enrol.c:456-457`; `GB_racegen.c:339-340`; `C/max.c:28-41` → 1100 per sector when liked, **0 if the race dislikes the (0,0) fallback sector**). Sector `resource/crystals/type/condition` keep their `makeplanet` values. `planet->info[P-1].popn/fuel/resource/destruct/tax` are not set (the `STARTING_INVENTORY` block `GB_racegen.c:343-353` is compiled out: the macro is defined nowhere).
8. **Government ship** (`enrol.c:369-438`; `GB_racegen.c:265-330`, identical): `shipno = Numships()+1` (file size; the dead-ship free list is **not** used); `Race->Gov_ship = shipno`; `planet->ships = shipno`; `nextship = 0`; `type = build_type = OTYPE_GOV` (26); position = star + planet position; `land_x/land_y` = capital; `owner = race = P`, `governor 0`, `tech 100.0`; from `Shipdata[OTYPE_GOV]` (`C/shipdata.h:79`): armor 20, `guns = PRIMARY`, primary = secondary = 10, primtype LIGHT, sectype NONE, max_crew 10, max_destruct 100, max_resource 500, max_fuel 1000, max_speed 0, build_cost 500; `size 100`, `base_mass 100.0`, `mass = 100 + 10*race.mass`, class "Standard", `popn 10`, fuel/destruct/resource/troops 0, `alive = active = on = docked = protect.self = 1`, `retaliate 0`, `damage 0` (ABIL_DAMAGE deliberately not applied, `enrol.c:425-426`), `whatorbits = whatdest = LEVEL_PLAN`, `storbits = deststar = star`, `pnumorbits = destpnum = pnum`, name "". This satisfies `governed()` (`C/doturn.c:494-504`).
9. **Governor.** `governor[0].active = 1`, `toggle.highlight = P`, `toggle.inverse = 1`, `toggle.color = 0` (`enrol.c:239-242`; `GB_racegen.c:163-166`); `money 0`; name unset; password typed in `enrol` (`:246-248`), fixed `"0"` in racegen (`GB_racegen.c:158`); `Race->governors = 0` (`GB_racegen.c:250`); governors 1..5 inactive. Later `appoint` copies scope from governor 0 (`C/shlmisc.c:178-185`); `cs` with no arguments jumps to the default scope (`C/cs.c:150-162`).
10. **Resources, tech, AP, treasury.** `tech 0`, `morale 0`, `turn 0`, discoveries/war/allied/points 0 (`enrol.c:268-274, 441`; racegen relies on `Bzero`). Money 0. Only the home star gets `AP = 5`; global AP stays 0. Per update the system AP grows by `APadd` (`C/doturn.c:481-492`) divided by 20 if not governed.
11. **Metadata.** racegen: name and password copied from 64-byte fields into 35-byte `RNAMESIZE` buffers unchecked (`GB_racegen.c:155-156`); `God = (priv_type == P_GOD)`, `Guest` likewise (`:153-154`); `translate[i-1]` for `i = 0..63` = 100 if `i == P || P == 1 || God` else 1 (`enrol.c:259-265`; `GB_racegen.c:180-186`; writes `translate[-1]`, an out-of-bounds store); stats from attributes (`GB_racegen.c:204-221`): `absorb`, `collective_iq`, `Metamorph = (race_type == R_METAMORPH)`, `pods`, `fighters`, **`IQ_limit = attr[A_IQ]` when `COL_IQ == 1`, else `IQ`**, `number_sexes`, **`fertilize = attr[FERT]*100`**, adventurism, birthrate, mass, metabolism.
12. **Records written, in order** (`enrol.c:438-468`; `GB_racegen.c:330-365`): `putship`, `putrace`, `putsector` (one sector), `putplanet`, `getstar`+`putstar`; signals HUP/INT/TERM/QUIT/TSTP blocked around the block (`enrol.c:356-363`; `GB_racegen.c:252-259`). `Filewrite` ignores short writes and errors (`C/files_rw.c:47-50`). No locking despite the comment in `files_shl.c:7-8`.
13. **Failure conditions.** Data file missing (`files_shl.c:103-156`, exit); cap (`enrol.c:101-104`; `GB_racegen.c:88-94`); first race not God (`GB_racegen.c:82-87`); bad racial index / planet letter (`enrol.c:111-114, 154-157`); no planet of the type (`enrol.c:205-217` retry or exit; `GB_racegen.c:139-145` unenrollable); critique errors (section 2.2 item 2) with the deity dialogue abort/enroll/fix/mail (`C/enroll.c:33-97`); record parse errors (`C/racegen.c:942-944`). **No** duplicate-name or duplicate-password check anywhere; login resolves by the first race whose password matches (`C/shlmisc.c:498-514`). **No** write-failure detection.
14. **Partial enrollment windows** (same order in both programs): W1 after `putship` (orphan government ship, unreachable from any orbit list); W2 after `putrace` (race exists with `Gov_ship` set, owns nothing: the CR-07 state, with `governed()` true while the planet has no owner); W3 after `putsector` (planet counts heal at the next update, the orbit-list link never heals); W4 after `putplanet` (star `explored` bit and the 5 AP permanently lost; `inhabited` heals). Enrolling while the server runs loses the planet, sector, star and ship-list writes at the next update because the server rewrites everything from memory (`C/doturn.c:285-286, 308, 340, 356`) and may later overwrite the ship slot.

### 2.3 Initial values (compact) **[V]**

| Field | Value | C source |
|---|---|---|
| `Playernum` | `Numraces()+1`, max 63 | `GB_racegen.c:81,88-94`; `vars.h:26` |
| `governor[0]` home/def level, system, planet | `LEVEL_PLAN`, star, pnum | `GB_racegen.c:159-161` |
| `governor[0].active`, toggles | 1; highlight P, inverse 1, color 0 | `GB_racegen.c:163-166` |
| `governor[0].password` (racegen) / money | `"0"` / 0 | `GB_racegen.c:158`; Bzero |
| `Race->governors` | 0 | `GB_racegen.c:250` |
| `tech`, `morale`, `turn`, discoveries, war/allied | 0 | Bzero `GB_racegen.c:150` |
| `conditions[0..9]` | planet's | `GB_racegen.c:168-169` |
| `translate` own / others | 100 / 1 (all 100 for player 1 and Gods) | `GB_racegen.c:180-186` |
| `IQ` or `IQ_limit` | `attr[A_IQ]` to one of them by COL_IQ | `GB_racegen.c:210-213` |
| `fertilize` | `attr[FERT]*100` | `GB_racegen.c:216` |
| `likes[i]`, `likesbest` | `compat/100`; last type with 100 and planet-cov 1.0 | `GB_racegen.c:225-229` |
| `Gov_ship` | `Numships()+1` | `GB_racegen.c:266-267` |
| sector owner/race, popn, fert, eff, troops | P, `number_sexes`, 100, 10, 0 | `GB_racegen.c:243-248` |
| planet popn, troops, maxpopn | `number_sexes`, 0, `maxsupport*Maxx*Maxy/2` | `GB_racegen.c:245,248,339-340` |
| planet `info[P-1].numsectsowned/explored`, `planet->explored`, `planet->ships` | 1 / 1 / 0 / shipno | `GB_racegen.c:334-336,268` |
| star `explored`, `inhabited` bit P, `AP[P-1]` | set, set, 5 | `GB_racegen.c:361-363` |
| ship (see item 8) | armor 20, guns 10/10, crew 10, dest 100, cargo 500, fuel cap 1000, speed 0, cost 500, size 100, base mass 100, mass 100+10·race.mass, tech 100, docked/alive/active/on/protect.self 1 | `GB_racegen.c:265-330`; `shipdata.h:79` |

Constants: `MAXPLAYERS` 64 `vars.h:26`; `LEVEL_PLAN` 2 `vars.h:23`; `OTYPE_GOV` 26 `ships.h:39`; `STARTING_POINTS` 1400, `MIN_PASSWORD_LENGTH` 4 `game_info.h:26-27`; `RTEMP` 0 … `OTHER` 9, `TOXIC` 10 `tweakables.h:31-41`; planet types `tweakables.h:241-248`; sector types `SEA..WASTED` 0..8 `tweakables.h:260-268`; `MAXGOVERNORS` 5 `tweakables.h:326`; `LIMIT_APs` 255 `tweakables.h:239`.

### 2.4 C quirks to decide on before porting **[V]**

D1 `memcpy(&race,&last,…)` argument order inverted relative to the documented intent (`C/racegen.c:35-36, 652, 683, 975, 989, 1017, 1304`; `C/enroll.c:150`), so the designer's "valid race to fall back on" logic is broken in the C text; D2 duplicate enrollment of the same in-memory race possible (`GB_racegen.c:74-79`); D3 unseeded racegen search; D4 uninitialised `FILE*` in `C/enroll.c:59-68`; D5 `translate[-1]` store; D6 unbounded `strcpy`/`scanf` into 35-byte buffers; D7 water compat unchecked; D8 no name/password uniqueness; D9 capital fallback (0,0) can yield `maxpopn 0`; D10 enrolling while the server runs loses writes; D11 `blah[8]` out-of-bounds cost read; D12 `ROUND` truncates toward zero for negative values; D13 metamorph covariance table retains three IQ covariances. **[R]** Port the documented intent for D1, D5, D6, D9 and D11; keep D12 and D13 as C does **or** decide explicitly (section 10, Q4), because fixtures captured from the C functions will encode them.

---

## 3. C++ reference comparison (`kaladron/galactic-bloodshed@b67e4c3b`)

### 3.1 Pipeline **[V]**

One service, `EnrollmentService::enroll_player` (`K/gb/creator/enrollment_service.cc:163-312`), fed by the `enrol` wizard (`K/gb/creator/enrol.cc`) or the `racegen` session (`K/gb/creator/racegen_session.cc`). Input `RaceEnrollmentSpec` (`K/gb/creator/creator.cppm:278-308`): name, password, governor password (default "0"), address, home planet type, optional preferred sector, optional **`capital_coords`**, optional **`target_planet`**, `candidate_stars` (ordered), `is_guest`, the eleven attributes, sector compatibilities as fractions (plated defaults 1.0), optional `likesbest`. Steps: `playernum = max_race_player()+1` **before validation and outside any transaction** (`:166`); non-rigorous `validate` then `points_remaining < 0` check (`:170-183`); `target_planet` used **unchecked** if given, else `find_suitable_planet` (`:188-204`); capital: `likesbest ?: preferred_sector ?: SEC_LAND` (`:207-208`, not the planet default), `capital_coords` honoured unverified, else three passes over a shuffled map (colonizable of that type → that type → any non-wasted → (0,0)) (`:213-233`); `build_race` plus `race.temp`/gases from the planet (`:239-244`); `create_race` (`:245`); `create_ship(OTYPE_GOV)` with tech 100, class "Standard", popn = max crew, mass = base + crew·race.mass, `land_on_planet` (`:250-271`); `Gov_ship` (`:274-275`); sector `colonize(P, number_sexes)`, fert 100, eff 10, planet info `numsectsowned 1`, `explored` flags, `popn`, `maxpopn = maxsupport*num_sectors/2` (`:278-294`); star explored, inhabited, `AP 5` (`:297-301`); `EnrollmentResult{player_num, star, pnum, capital_coords, gov_ship}` (`creator.cppm:349-357`).

**Persistence:** SQLite via an `EntityManager` that saves **immediately per entity** (`K/gb/services/entity_manager.cc:126-136, 158-187, 204-212, 253-273`); `Transaction` and `DeferredWriteScope` exist (`:1173-1297`) but **no enrollment caller uses them**. Enrollment is seven or more autocommits (race, block, power, ship, race again, sector map plus planet, star) and an exception mid-way leaves a partial enrollment with no rollback. The C++ therefore does **not** solve the atomicity question for Go.

### 3.2 Race generation engine **[V]**

Attribute parameters identical to C in every column (`K/gb/creator/racegen_engine.cc:230-329` vs `C/racegen.c:192-249`); planet costs, sector base cost, both covariance tables, the `> 1.01` rule and the 1400/4 constants identical (`racegen_engine.cc:96-226, 460-483`; `K/gb/entities/tweakables.cppm:13-14`). Deviations: **rounding** with `std::lround` (`:446, 490`) instead of C's truncating `ROUND` (Collective IQ costs -351 in C++, -350 in C); **`l_fudge` kept unrounded** (`:380-390`); **metamorph covariance matrix omits** the three IQ covariances C retains (section 2.2 item 2); **`sector_count_costs[9]` adds 600 for eight types and clamps** (`:121-123, 495-496`), a deliberate correction of C's out-of-bounds read, documented in `K/docs/race_generation.md:126-135`. Validation (`:504-621`) matches C's non-rigorous rules, moves the empty-name check to rigorous, drops C's mutating truncations, adds "metamorph must have one sex" (`:534-536`), checks all eight compatibilities including water (`:555-569`) and rejects asteroids as homes; **the enrollment path never runs the rigorous rules** (`enrollment_service.cc:170`, `racegen_session.cc:377`), so short passwords, empty names and plated-only compatibilities enrol.

### 3.3 Archetypes (all new relative to C) **[V]**

`K/gb/creator/creator.cppm:123-267` defines eleven presets: Metamorphic Predator (Forest), Metamorphic Heavyweight (Desert), Metamorphic Colossus (Mars), Cerebral Researcher (Earth), High IQ Scholar (Water), Progressive Technocrat (Earth), Balanced Expansionist (Earth), Adaptive Explorer (Iceball), Aggressive Colonizer (Forest), Militaristic Legionnaire (Mars), Jovian Gas Floater (Gas giant), with mass/birthrate/fighters/IQ/adventurism/sexes/metabolism per preset and random deltas identical to `C/enrol.c:53-59` but clamped. `to_enrollment_spec` (`K/gb/creator/archetypes.cc:16-68`) pre-fills planet-default compatibilities and lowers metabolism in 0.05 steps until the budget is met. **[R]** Treat as an optional onboarding feature for the TypeScript client, after parity; values must be re-validated against the Go cost function before adoption.

### 3.4 Homeworld, colony, ship, governor versus C **[V]**

Search: shuffle (mt19937, thread-local seed 1337, `K/gb/entities/rand.cc:15-23`) or caller order; skips inhabited stars and stars with fewer than two planets; type match; **planet-level vacancy check** like `enrol.c`; **no temperature window** (tests accept -120 °C and +150 °C, `K/gb/creator/enroll_test.cc:241-292`). Colony values (owner, popn = sexes, fert 100, eff 10, troops 0, planet info, maxpopn, star bits, AP 5) equal C. Differences: `translate` others 0 instead of 1 (`K/gb/entities/race.cppm:117-118`); government ship name "Govrnmnt. Center", computed size/mass/build cost instead of C's 100/100.0/500, `retaliate` true instead of `protect.self`; governor 0 named "Leader", ship governor id 1; a seeded Deity race (`enrollment_service.cc:28-62`) created by universe generation instead of C's "first race must be God" rule; **no MAXPLAYERS cap** (tests enrol player 65, `K/gb/creator/enrollment_test.cc:103-133`).

### 3.5 Test patterns worth reusing conceptually **[V]**

In-memory database per test; fluent star/planet builders with chosen sector maps; **assertions on resulting world state** (race fields, ship, planet, sector, star) rather than on the API result (`enrollment_test.cc:157-259`); ordered `candidate_stars` to make the search deterministic (`enroll_test.cc:188-298`); negative cases for no planet of the type, out-of-range attribute, missing plated, over budget (`enrollment_test.cc:135-155, 476-549`); regression constants for the cost function (default race cost 175, `racegen_test.cc:14-41`); an archetype sweep across planets and random rolls (`racegen_test.cc:227-335`); JSON round trip and re-enrollment. Gaps there: no gov-ship numeric assertions, no partial-failure or rollback test, no duplicate-name or concurrency test.

### 3.6 Unresolved C++ concerns **[V]**

No transaction; rigorous rules unenforced; self-defeating privilege check (`racegen_session.cc:377, 675`); `pref` default `SEC_LAND` versus planet default; unvalidated `target_planet`/`capital_coords`; player-number race and no cap; ±1 cost differences from rounding; metamorph covariances; temperature filter dropped; `translate` 0; percent/fraction input ambiguity (`racegen_session.cc:486-489`); `enrol.cc` writes `racegen.json` with plaintext passwords to the working directory (`:283-289`). `K/docs/race_generation.md` is accurate about tables and formulas except that it says floor where the code rounds and describes "1000 colonists" and a "Battleship" flagship that the code does not create.

---

## 4. Current Go implementation: findings and confirmed defects

### 4.1 Call graphs **[V]**

**CLI** (`go/cmd/racegen/main.go`): `main` `:617` → `initNormalCov` `:78` → `defaultRace` `:142` → `initFudges` `:159` → REPL `:633`; `enroll`/`save` `:665` → `validate(&r, true)` `:261-330` → `db.Open(-db, default ./gb.db)` `:676` (bbolt exclusive lock, 5 s timeout, `go/internal/db/store.go:24-35`) → `AllRaces` `:683` → lowest free slot 1..64 `:689-697` → `buildModelRace` `:556-611` → **`store.PutRace`** `:700` (one record, one transaction) → close. It runs outside the server, bypasses the actor, the preview gate and authentication, and fails with a lock timeout while the server runs (`go/README.md:70`).

**Web/actor:** `GET /enroll` and `POST /api/enroll` (`go/internal/net/server.go:127-128`) → `handleEnrollPage` `:265-277` / `handleEnroll` `:281-310` (gate `!cfg.EnrollPreview` → 403; 16 KiB body limit; 10 s context; **no authentication, rate limit or token**) → `GameIface.Enroll` → `State.Enroll` (`go/internal/game/admin.go:348-403`): second gate `:349-352`; `call(opEnroll)` → `submit` (`lifecycle.go:403-445`, reply channel only, GB1-01) → `actorLoop` (`state.go:376-378`) → `handleAdmin` (`admin.go:53-67`). On the actor: mode must be `SERVING` `:355` (so refused during a turn and while QUIESCED, consistent with CR-43); lowest free slot in `s.races` `:358-364`; case-insensitive duplicate name `:365-369`; `enroll.Enroll` (`go/internal/enroll/enroll.go:38-150`); **`store.PutRace`** `:377`; `s.races[P-1] = race` `:380`; `publishWorld` `:381`; response with `EnrollPreviewNotice`. HTTP mapping: 403/503/500 ("Enrollment failed", the store error detail is dropped `server.go:303-304`)/422/200. Metrics `gb_enrollments_total{result}` (`metrics.go:29,59`). `Authenticate` runs on the actor against `s.races`, so a preview race can log in without restart (`lifecycle_test.go:540-544`).

### 4.2 Classification per behaviour **[V]**

| # | C behaviour | Go (web / CLI) | Classification |
|---|---|---|---|
| a | player number `Numraces()+1`, cap 63, first race God | lowest free slot 1..64 (`admin.go:358-364`; `main.go:689-697`); no God/Guest concept in requests | PARTIALLY IMPLEMENTED; exhaustion untested; CLI untested |
| b | attributes, costs, budget, validation | **CLI:** tables and cost code match `racegen.c` (`main.go:60-126, 170-255`) except: `math.Round` vs C truncation; only `normalCov` exists and is applied to metamorphs (`:78-93`); `Fertilize: uint8(attr)` truncates 0..1 to 0/1 (`:574`, C ×100); `IQ` and `IQLimit` both set (`:575-576`); `LikesBest` set to a **planet-type** constant via `homePlanetMap` (`:591-600`) although the field is a sector type; Airless → `PlanetAsteroid` (C → Mars). **Web:** range checks only, **no point budget**, no compat rules (`enroll.go:41-93`); ranges differ from C (IQ 1-100, BirthRate 0.25-1, Mass 0.1-10, Metabolism 0.1-10, Sexes 1-15); **Adventurism stored as 0-100** while the engine treats it as a fraction (`planet.go:761,764`); **Fighters absent → 0** (engine strength 10·Fighters, `commands_profile.go:614,622`); no Absorb/Pods/CollectiveIQ/Metamorph; password ≥ 3 (C 4); `Conditions` fixed at 50 (`enroll.go:136-139`) | CLI PARTIALLY IMPLEMENTED / INCORRECT, UNVERIFIED (no tests); web INCORRECT |
| c | homeworld search | none | MISSING |
| d | occupied-world avoidance | none (model has `Star.Inhabited`, `PlInfo.NumSectsOwned`) | MISSING |
| e | star/planet assignment, governor scope | `Governor[0].Home*/Def*` left 0 (`enroll.go:143-147`); star bits, AP, planet info not set | MISSING |
| f | capital sector | none (`Sector{Owner,Race,Popn,Fert,Eff}` and `Planet.SectorAt` exist, `model/planet.go:55-67,111`) | MISSING |
| g | population, troops, maxpopn | none (`maxSupport` exists, `game/planet.go:1180`, golden fixture `test/golden/testdata/max_support.csv`) | MISSING |
| h | government ship, orbit list, `GovShip` | none; `Race.GovShip` 0 (`getshipDefaults` `build.go:140-181`, `initializeNewShip` `:427-524`, `insertShipPlan` `planet.go:1365`, `allocShipNum` `commit.go:204-228`, `OtypeGov` `constants.go:58`, `Shipdata[Gov]` `shipdata.go:38` exist) | MISSING |
| i | governor defaults | `Name = race`, `Password = race hash`, `Active`, **`Governors = 1`** (`enroll.go:142-147`; `main.go:603-608`); C: password "0", scope, toggles, `governors 0`. Side effect: `authenticate` (`state.go:762-770`) admits governor ≤ `Governors` and skips the password check when the governor password is empty, so **governor 1 can log in with the race password** (read, not executed) | PARTIALLY IMPLEMENTED + INCORRECT |
| j | tech, morale, AP, money, conditions, translate | zeros coincide; AP, planet conditions, translate missing (`translate` 0 vs C 1) | PARTIAL / INCORRECT / MISSING |
| k | metadata updates | none | MISSING |
| l | records and transactions | one `PutRace` (`store.go:303-314`), no `CommitWorld`, no commit record | IMPLEMENTED AND TESTED for the one record (`lifecycle_test.go:536-539`); the other four MISSING |
| m | failure conditions | gate, slot, duplicate name, validation, not serving, engine busy, decode, write error (detail dropped) | IMPLEMENTED AND TESTED for existing paths (`server_test.go:296-330`, `lifecycle_test.go:506-558`, `remediation_test.go:321-343`); no test for exhaustion, write failure, non-serving modes |
| n | partial enrollment | trivially atomic (one record) but the result **is** the defect; no `DeleteRace` exists; a request whose caller times out after the actor started still stores the race (`admin.go:388-392`) | IMPLEMENTED, state itself defective |

Other **[V]** points: bcrypt failure falls back to **storing the plaintext password** (`enroll.go:97-101`; `main.go:557-562`); `LikesBest` assigned twice in the CLI (`main.go:581, 600`); the web form's defaults include plated 25 which C forbids for non-Jovians (`enroll_page.go:183-298`) and its sector labels still mismatch the model's sector indices (CR-08, `go-code-review.md:223`); the admin listener and `gbctl` have no enrollment operation (`cmd/gbctl/main.go:67`); `Race.God`/`Guest` exist in the model but nothing sets them, so a Go universe never has the God race C requires first.

### 4.3 Model and storage support **[V]**

Every field the C writes exists in the Go model: `Race` (`model/race.go:46-104`: traits, `Conditions [11]int16`, `Likes [9]float64`, `LikesBest uint8`, `Translate [64]int8`, `GovShip`, `Governors`, `Governor [6]`), `Governor` (`:20-39`: `Def*`, `Home*`, `Toggle`, `Money`), `Planet` (`model/planet.go:72-108`: `Ships` orbit head, `Info [64]PlInfo` with `NumSectsOwned/Explored/Popn`, `Popn`, `Troops`, `MaxPopn`, `Explored`, inline `Sectors`), `Sector` (`:55-67`), `Star` (`model/star.go:8-30`: `AP [64]`, `Explored`, `Inhabited` with setters `:38,48`), `Ship` (`model/ship.go:111-213`: every field of item 8). Sectors are stored inside the planet record (no sector bucket, `db/keys.go:9-23`; `store.go:201-213`). **No schema or record-format change is required** to store a C-faithful enrollment. Store APIs: per-record `Put*`, `AllocShipNum` (`store.go:154-181`, free-list pop or max+1, fenced, own transaction), `PutUniverse` (`db/meta.go:137-190`, whole universe in one transaction with an in-transaction refusal), `CommitWorld` (`db/commit.go:342-476`, whole world plus schedule, news, telegrams, free-list pops and the commit record in one transaction; `validate()` `:317-335` accepts only kinds `update|segment|shutdown`, `knownCommitKinds` `:58`), write fence `SetWriteFence`/`writeAllowed` (`:177-201`), `GetCommitRecord`, `WorldDigest` (`:573`), fault hooks `SetCommitFault` (`:210`) and `SetMetaFault` (`meta.go:403`). Missing: a multi-record single-transaction writer other than those two; `DeleteRace`; any idempotency record.

### 4.4 Actor plumbing to reuse **[V]**

`opEnroll` and the typed request path already exist (`admin.go:22-33`, `lifecycle.go:403-461`); `InitUniverse` (`admin.go:307-339`) is the model of "build in memory, write in one transaction, reload or halt"; `persistTurn` (`game/commit.go:269-360`) classifies outcomes by reading the record back; `beginTurnBody/endTurnBody` and the staged allocator (`commit.go:102-228`) show how numbers are allocated inside a commit; `initializeNewShip` emits player notices directly unless `effects.active` (a quiet variant is needed); `publishWorld` refreshes status.

### 4.5 Reusable tests and helpers **[V]**

`newTestEngine` (`game/lifecycle_test.go:38-65`: temp bbolt, `universe.Init{Stars 4, MinPlanets 1, MaxPlanets 2}` seed 11, fixture race 1), `fixtureRace` `:67-76`, `reopen` (`commit_test.go:99`), `store.Counts`, `WorldDigest`, fault hooks, `testHooks` (`lifecycle.go:464-480`), e2e `startServer`/`addFixtureRace` (`test/e2e/basic_test.go:122, 258-273`); golden harness `TestGolden` (`golden_test.go:60`) with `max_support.csv` and `compatibility.csv`; the inventory enforcement (`inventory_test.go`, `testutil/knowndefects.go`). Existing enrollment tests: `TestEnrollmentDisabledDoesNotTouchStore`, `TestGB101WelcomeEnrollInitTimeouts/enroll`, `TestEnrollmentDisabledByDefault`, `TestOriginsAndPreview`.

**[V] Inventory:** `test/knowndefects.yaml` has **no CR-07 entry** (grep for `CR-07`, `enrol`, `homeworld`, `gov ship` returns nothing); CR-07 is tracked only in `go-code-review.md` 4.8 and the migration plan. No inventory change is therefore required to implement it; adding one would need the `known-defect-added` label.

---

## 5. Proposed Go architecture and atomic persistence approach **[R]**

### 5.1 Placement inside the engine, as one actor request

Keep ADR-0001: one authoritative engine, one consistency boundary. Enrollment stays the typed actor request `opEnroll`, so it is serialised against turns, admin operations and other enrollments, refused outside `SERVING` (hence refused while QUIESCED, which keeps CR-43 semantics: a quiesced server changes nothing), and covered by the existing panic-to-HALTED guard. The request carries a **client-generated `enrollment_id`** (UUID, plan 6.7) so that an uncertain outcome is resolved by repeating the same request.

Inside the actor, in this order:

1. **Validate without touching state:** mode `SERVING`; request schema; the full race-design rules (ranges, booleans, compat rules, plated-100, gas rules, ≥ 2 sector types, 100% compat on a common sector) and the **point budget** from the shared rules package (work unit A); name uniqueness (case-insensitive, existing); password policy.
2. **Idempotency check:** if `meta/placement:<enrollment_id>` exists, return the stored placement and the current status of that race (no new work).
3. **Allocate the player number** by the chosen rule (section 10, Q1), checking the cap.
4. **Pure placement** (work unit B): search for the home planet over the in-memory stars and planets with an injected order/RNG; choose the capital sector; produce **new copies** of the race, the home planet (with its one changed sector), the home star and the government ship, plus the ship number to reserve. Nothing in `State` is mutated yet.
5. **One transaction** (work unit C): `store.PutEnrollment(race, planet, star, ship, placement)` writes the four records, reserves the ship number inside the same transaction (free-list pop with tail verification exactly as `popFreeNumsTx` does for turns, or max+1), writes `meta/placement:<id>` and returns. The write fence is respected (`writeAllowed`), so the method cannot run during a turn body even if a future bug reached it.
6. **Only after the transaction returned without error** the actor installs the copies into `s.races`, `s.planets`, `s.stars`, `s.ships`, rebuilds the planet's orbit chain head (the ship is the only docked ship on a vacant planet), calls `publishWorld`, counts `gb_enrollments_total{result="ok"}` and replies with the placement (player number, star, planet, capital coordinates, ship number).
7. **Failure classification:** if the transaction reports an error, read `meta/placement:<id>` back (the same technique CR-18 and CR-43 use): absent → nothing written, reply with a retryable error; present → the commit became visible despite the error, install the copies from the store (reload those four records) or, if that fails, halt with an explicit reason. Never reply success before the read-back agrees. A caller whose context expired after the actor started receives `ErrStillRunning` today (`admin.go:388-392`); with the idempotency key the client simply repeats the request and gets the stored placement.

### 5.2 Why not a new CR-18 commit kind, and why not wait for CR-17

**[V]** `CommitWorld` validates a closed set of kinds (`db/commit.go:51-58, 317-335`) and startup rejects an unknown kind as `CORRUPT_STORE` (`CommitRecord.Validate` `:86`; `game/commit.go:466`). An `enroll` commit record written by a CR-07 image would therefore make the store unreadable by the **currently deployed image** (`sha256:d62c1fe6…`), turning every image rollback into a `HALTED` start. **[R]** Do not add a commit kind for CR-07. Enrollment is a between-turn write like a command; CR-18 covers turns and the shutdown flush, and the plan's section 8.4 (per-command staging and group commit, CR-17) is where all between-turn writes will eventually go. A dedicated `PutEnrollment` transaction is the narrowly scoped atomic write the brief asks for: it needs no commit record (the schedule and counters do not change, so the CR-18 startup alignment check is unaffected), no change to `WorldDigest` semantics (races, planets, stars and ships are already digested), and only an **additive** meta key. **[A]** Verify that strict startup validation ignores unknown `meta` keys (it validates `meta/commit`, `meta/schedule`, `meta/maintenance` and `meta/universe` individually; an unknown key is not read) before relying on rollback safety, and add a test that the pre-CR-07 reader opens a store with placements (test with the current `db` package against a fixture).

CR-17 is therefore **not a dependency**: CR-07 can ship before the staging set exists. When CR-17 lands, `PutEnrollment` becomes one more staged batch and the idempotency record becomes the dedup entry; the request and response shapes do not change.

### 5.3 Rules that need an owner decision (section 10)

Player-number rule (append-only `highest+1` with cap 63 as C, or lowest free slot as today); God race (none today; C requires the first race to be God); temperature window (racegen ±[-200,100] versus `enrol` ±50) and planet-level vacancy (recommend: racegen attributes with the stricter `enrol`/C++ vacancy check and the racegen window); rounding and metamorph covariance convention (C-exact for fixtures); the government ship number source (C: `Numships()+1`; Go allocator: free list first); governor 0 password (C racegen "0" versus a supplied password); the gate name once races are playable; authentication of the endpoint before Phase 6.

### 5.4 Interfaces **[R]**

- **Engine API (Go-owned contract, versioned later under `api/openapi/gb-v1.yaml`):** `POST /api/enroll` with `{enrollment_id, name, password, governor_password?, address?, home_planet_type, race_type, attributes{...11}, sector_compat{8}, preferred_sector?}`; `GET /api/enroll/options` (attribute definitions with min/init/max/integral, planet costs, sector list, budget, rules text); `POST /api/enroll/cost` (pure costing for live feedback: returns per-attribute and per-sector costs, total, remaining, structured errors); `GET /api/enroll/{enrollment_id}` (placement or 404). Errors structured as `{code, field, message}` with an enumerated `code` set (`OVER_BUDGET`, `ATTR_OUT_OF_RANGE`, `NO_HOME_PLANET`, `NAME_TAKEN`, `SLOTS_FULL`, `NOT_SERVING`, `COMMIT_FAILED`, …), matching plan section 6 (`microservices-migration-plan.md:419`).
- **Operator path now:** `gbctl enroll -f race.json` through the authenticated loopback admin listener (same bearer token, same request type), which gives an authenticated enrollment path before identity exists and lets the owner enrol the God race. `racegen` becomes a designer that writes the JSON spec and submits it through either path; it **stops opening the database** (removes the second-handle violation of CR-03 and the "server must be stopped" rule).
- **Web form:** stays behind the gate (renamed once races are playable, Q8); fix CR-08 labels; costed live through `/api/enroll/cost`.

### 5.5 Reuse rather than rewrite **[V]/[R]**

Reuse: the cost tables and `totalCost`/`validate` from `cmd/racegen` (move to an importable package); `getshipDefaults` and `initializeNewShip` (with a quiet variant) and `Shipdata`; `insertShipPlan`; `maxSupport`; `intRand` or the engine RNG; the actor request plumbing, `publishWorld`, metrics; `PutUniverse` as the template for the single-transaction writer and `popFreeNumsTx` for in-transaction number reservation; `reopen`, `WorldDigest`, fault hooks and `newTestEngine` in tests. Rewrite: `enroll.Enroll`'s field validation (incomplete and wrong scales) and both request structs.

---

## 6. Work units in dependency order **[R]**

Each unit is a separate branch and pull request from a worktree, independently reviewed, all local gates green, hosted CI green. Sizes are relative. **"Format change"** means a change to what is stored or how it is read.

### WU-A: shared race-design rules package (`go/internal/racegen`)

- **Goal:** one importable implementation of attributes, costs, budget and validation, C-exact, used by the CLI, the engine and the API.
- **Files:** new `go/internal/racegen/{attributes.go, cost.go, validate.go, spec.go}` and tests; `go/cmd/racegen/main.go` reduced to I/O over the package; `go/internal/enroll/enroll.go` request type replaced by `racegen.Spec` (or the enroll package deleted in WU-D).
- **Content:** tables from `C/racegen.c:192-304`; `cost_of_race` with the **C rounding convention** and integer `l_fudge` (Q4); `normal()` and `metamorph()` covariances as C applies them (including the retained IQ covariances, Q4); `blah` surcharge with the eight-type case defined (Q4); the full `critique_to_file` rule set in non-mutating form (report truncations as errors or normalise explicitly), including the water compat check; `Fertilize = attr*100`, `IQ`/`IQLimit` by Collective IQ, `LikesBest` = last 100%-compat sector type with planet-cov 1.0, `planet_translate` with Airless → Mars.
- **Tests:** golden fixtures generated from the C: extend `test/ctool` to compile `cost_of_race`, `critique_to_file` tables and `planet_translate` (racegen.c depends only on its headers and `game_info.h`; **[A]** confirm it compiles in the tool without curses), producing `test/golden/testdata/racegen_cost.csv` for a table of races (defaults, each archetype of section 3.3 as extra rows, boundary values, metamorphs); table tests for every validation rule; property test that `init` values cost 0.
- **Acceptance:** every fixture row matches to the point; default race costs 175 with 1225 remaining (the C++ regression constant, to be confirmed against the C fixture); `go vet`, lint baseline unchanged.
- **Format change:** none.

### WU-B: pure placement rules (`go/internal/game/placement.go`)

- **Goal:** deterministic functions that, given the in-memory world, a race spec, a player number and an injected star order (or RNG), return the new race, planet, star and ship copies and a reason on failure.
- **Files:** new `placement.go`, `placement_test.go`; small helpers in `build.go` for a quiet `initializeNewShip`; `model` untouched.
- **Content:** home search per section 2.2 items 3 and 4 with the decided window and vacancy rule (Q2); capital sector per item 6 with the documented-intent fix for D9 (Q5: if no sector of `LikesBest` exists, fail with `NO_CAPITAL_SECTOR` rather than (0,0)); initial values per table 2.3; government ship per item 8 built from `Shipdata` (ship number supplied by the caller); governor 0 per item 9 (Q6 password); translate per item 11; `MaxPopn` via `maxSupport`.
- **Tests:** fixture universe from `universe.Init` with a fixed seed; expected home planet for each home type given a fixed order; star skipped when inhabited or single-planet; vacancy rejects a planet with any `NumSectsOwned`; temperature window edges; no candidate → `ErrNoHomePlanet` and no mutation of inputs; capital sector choice with and without a liked sector; every initial value of table 2.3 asserted on the returned copies; the ship satisfies the Go `governed()` predicate; `maxpopn` arithmetic; inputs are not aliased (mutation of outputs leaves `State` unchanged).
- **Acceptance:** 100% of table 2.3 covered by assertions; no RNG global used.
- **Format change:** none.

### WU-C: single-transaction store method (`go/internal/db`)

- **Goal:** `Store.PutEnrollment(e Enrollment) error` writing race, planet, star, ship, reserving the ship number and writing `meta/placement:<id>` in one `bolt.Update`, fenced, with fault hooks.
- **Files:** `db/enrollment.go` (new), `db/keys.go` (placement key prefix), `db/commit.go` (share the free-list pop helper), tests `db/enrollment_test.go`.
- **Content:** refuse if the race slot already holds a record or the placement id exists (in-transaction checks, like `PutUniverse`); ship number: pop the free list tail or `max+1` inside the transaction (Q7); strict-encode the placement record `{schema_version 1, enrollment_id, player_num, star, planet, capital{x,y}, gov_ship, wall_time}`; `GetPlacement(id)`; fault stages before the transaction, inside it and after commit (`SetMetaFault` style) for tests.
- **Tests:** all-or-nothing under each fault stage (`WorldDigest` and `Counts` unchanged, placement absent, free list unchanged); reopen shows all four records and the placement; a second call with the same id is refused; a second call for the same slot is refused; fence refuses the call during a turn body (`SetWriteFence(true)`); free-list tail verification mismatch fails the transaction; **compatibility:** a store containing a placement opens and validates with the existing strict startup checks (and document that the deployed image's `db` package ignores the new key).
- **Acceptance:** `go test -race ./internal/db` green; no new lint findings.
- **Format change:** **additive meta key only**; no change to existing records, kinds or the digest definition. Record it in `docs/operations/local-server.md` section 9 (store format) and ADR (see WU-F).

### WU-D: engine enrollment on the actor (`go/internal/game/admin.go`)

- **Goal:** `State.Enroll` performs steps 1 to 7 of section 5.1.
- **Files:** `admin.go` (replace the body), `state.go` (install helpers: add race, planet copy, star copy, ship; orbit chain), `metrics.go` (result labels), `engine/engine.go` (errors: `ErrNoHomePlanet`, `ErrSlotsFull`, `ErrNameTaken`, `ErrEnrollmentNotDurable`…), delete `go/internal/enroll` or reduce it to the request type; tests in `game/enrollment_test.go`.
- **Content:** validation via WU-A; idempotency; allocation rule (Q1) and cap; placement via WU-B with the engine RNG (`intRand` today, Phase 3b RNG later: isolate behind one function); `PutEnrollment`; read-back classification; install into memory only after success; `publishWorld`; structured errors; preview notice removed; `Authenticate` sees the race at once.
- **Tests (world state, disposable store, seeded universe):** happy path asserts every field of table 2.3 on `s.races/planets/stars/ships` **and** on the store after `reopen`; `governed()` true; login as governor 0 then `cs` with no arguments lands at the home planet; `survey`/`map` of the home planet works; `capital` reports the ship; second enrollment gets the next number and a different vacant planet; same `enrollment_id` twice → identical placement, one race; name collision → `NameTaken`, nothing written; over budget / invalid attributes → nothing written; slots full → nothing written; no home planet (fixture with all stars inhabited or single-planet) → nothing written, digest unchanged; fault inside the transaction → nothing written, slot free, repeat succeeds; fault after commit → read-back finds the placement, memory installed, success returned, no double race; request during `TURN_IN_PROGRESS`/`QUIESCED`/`HALTED` → `ErrNotServing`, nothing written; enrolled race survives orderly restart and a following turn (`commit_seq` advances, race still governed); `-race` detector clean.
- **Acceptance:** the acceptance test of plan Phase 3c (`microservices-migration-plan.md:1412`) passes: home planet, owned sector, government ship, `GovShip != 0`, login without restart, idempotent re-placement.
- **Format change:** none beyond WU-C.

### WU-E: interfaces (`gbctl enroll`, `racegen` as client, HTTP contract, web form)

- **Files:** `go/internal/admin` (new `enroll` route, token-authenticated), `go/cmd/gbctl/main.go` (`enroll -f spec.json`, exit codes consistent with the existing table), `go/cmd/racegen` (designer that saves/loads `racegen.json`-style spec and submits via `-server` or writes the spec file; database access removed), `go/internal/net/server.go` and `enroll_page.go` (JSON contract of section 5.4, `options` and `cost` endpoints, CR-08 label fix, rate limit and body limit), docs.
- **Tests:** `cmd/gbctl` envelope tests for the new operation; `net/server_test.go` for options/cost/enroll/placement routes with the fake engine; `racegen` spec round trip; e2e below.
- **Acceptance:** owner can enrol a race against a local server with `gbctl enroll`; web form shows live cost from the server; no code path opens the database from a second process.
- **Format change:** none (the spec file is a client artifact).

### WU-F: end-to-end, documentation, operations

- **Files:** `go/test/e2e/enrollment_test.go` (subprocess: start server, init universe, enrol via admin API, login over WebSocket, `cs`/`survey`/`capital`, SIGTERM, restart, assert the same race, ship and sector; SIGKILL inside the enrollment transaction via the fault hook → nothing written); `.github/scripts/kind-e2e.sh` optional step (enrol one race through a port-forward and check `governed` via status); docs: new ADR (enrollment transaction and idempotency; decisions of section 10), `docs/operations/local-server.md` section 6 (rewrite), `docs/operations/deploy.md` player-access section, `docs/architecture/go-code-review.md` addendum closing CR-07/CR-08/CR-09 with evidence, `CLAUDE.md` status paragraph, `docs/testing/cr07-handoff.md` (results).
- **Acceptance:** hosted CI green including e2e; documentation reviewed.
- **Format change:** none.

### WU-G (optional, separate approval): archetypes and client conveniences

Presets as data served by `GET /api/enroll/options` (validated against WU-A at startup), planet-default compatibilities, a "fit to budget" helper; no engine rule changes. TypeScript consumption is Phase 7.

### Dependency graph

A → B → C → D → E → F; G after E. A and C can be developed in parallel (C only needs the model); B needs A only for `LikesBest`/`Fertilize` derivations (can start with a stub). Estimated total: a few hundred lines of engine code plus roughly twice that in tests, comparable to CR-18 (`docs/testing/cr18-handoff.md:104`).

---

## 7. Files expected to change (summary)

| Work unit | New | Modified | Deleted / reduced |
|---|---|---|---|
| A | `go/internal/racegen/*.go`, `test/golden/testdata/racegen_cost.csv`, `test/ctool` additions | `go/cmd/racegen/main.go` | duplicated tables in `cmd/racegen` |
| B | `go/internal/game/placement.go`, `placement_test.go` | `go/internal/game/build.go` (quiet ship init) | — |
| C | `go/internal/db/enrollment.go`, `enrollment_test.go` | `go/internal/db/keys.go`, `commit.go` (shared helper) | — |
| D | `go/internal/game/enrollment_test.go` | `admin.go`, `state.go`, `metrics.go`, `engine/engine.go` | `go/internal/enroll` |
| E | admin route, `gbctl enroll`, net routes | `cmd/gbctl/main.go`, `cmd/racegen/main.go`, `internal/net/server.go`, `enroll_page.go`, `internal/admin/*` | CLI database access |
| F | `go/test/e2e/enrollment_test.go`, ADR, `docs/testing/cr07-handoff.md` | `local-server.md`, `deploy.md`, `go-code-review.md`, `CLAUDE.md`, optionally `.github/scripts/kind-e2e.sh` (label `gate-change-reviewed`) | — |

Protected gate tooling (`.github/`, `test/scripts/`, `go/internal/testutil/`, `test/ctool/`) changes need the `gate-change-reviewed` label; `test/ctool` fixture generation in WU-A falls under it. The known-defect inventory is not changed by this plan (no CR-07 entry exists).

---

## 8. Deterministic test matrix and acceptance criteria

Fixtures: disposable bbolt file per test; `universe.Init` with fixed seeds (seed 11 as in `newTestEngine`, plus a seed chosen so that every home type has at least one candidate, found by a one-off scan and pinned); injected star order for placement tests; C fixtures for costs; `WorldDigest` before/after for every negative case; `reopen` for every positive case.

| # | Case | Asserts (world state, not API) | Unit |
|---|---|---|---|
| P1 | normal race, default attributes, Earth | all of table 2.3 in memory and after reopen; `governed()`; login; `cs` → home planet | D, F |
| P2 | each home type (7) with a fixed star order | planet type matches `planet_translate`; temperature within window; star uninhabited with ≥ 2 planets | B |
| P3 | capital sector | first sector of `LikesBest` type in the injected order; owner/race/popn/fert/eff/troops; planet counts; `MaxPopn` | B |
| P4 | government ship | every field of item 8; `Planet.Ships == ship`; `NextShip == 0`; `Race.GovShip`; ship number reserved in store | B, C, D |
| P5 | governor defaults | `Governor[0]` scope, toggles, active, password per Q6; `Governors` per C (0) and `authenticate` refuses governor 1 | D |
| P6 | starting resources | `AP[P-1] == 5` on the home star only; tech/morale/money 0; conditions copied; translate 100/1 | B, D |
| P7 | first permitted actions | after login: `survey`, `map`, `capital`, `cs` scope prompt; no AP spent beyond C rules | D, F |
| P8 | restart persistence | SIGTERM/restart: same race, ship, sector, star bits; a following turn commits with the race governed | F |
| N1 | no suitable homeworld | every star inhabited or single-planet → `NO_HOME_PLANET`; digest unchanged; slot free | B, D |
| N2 | invalid attributes | each range, boolean, gas rule, plated rule → structured error, nothing written | A, D |
| N3 | over budget | `OVER_BUDGET` with remaining points; nothing written | A, D |
| N4 | maximum race count | 63rd (or 64th per Q1) refused `SLOTS_FULL`; nothing written | D |
| N5 | occupied target | planet with `NumSectsOwned > 0` or star inhabited skipped; if the request pins a target (if such a field exists), refused | B |
| N6 | invalid capital | no sector of `LikesBest` → `NO_CAPITAL_SECTOR` (Q5), nothing written | B, D |
| N7 | duplicate enrollment | same `enrollment_id` twice → same placement, one race; same name different id → `NAME_TAKEN` | D |
| N8 | database write failure | fault before/inside transaction → nothing written, placement absent, retry succeeds | C, D |
| N9 | mid-enrollment failure and rollback | panic in placement or install → actor survives (or halts per policy), store unchanged | D |
| N10 | ambiguous response | fault after commit / caller timeout → read-back finds placement; repeat with the same id returns it; no double race | C, D |
| N11 | consistency after reload | `reopen` and `loadAll` produce equal digest and `Counts`; `validateStoreCommit` passes | C, D |
| N12 | mode guards | `TURN_IN_PROGRESS`, `QUIESCED`, `HALTED`, `UNINITIALIZED` → `ErrNotServing`, nothing written | D |
| N13 | fence | `PutEnrollment` during a turn body refused and counted | C |
| N14 | compatibility | store with placements validates under current strict startup; pre-CR-07 reader ignores the key | C |
| G1 | C cost fixtures | every row of `racegen_cost.csv` equal | A |
| G2 | archetype rows (optional) | each preset within budget under the Go cost function | A/G |

**Acceptance criteria for CR-07 (Definition of Done, section 9)** are the union of P1-P8, N1-N14 and G1, plus the Phase 3c acceptance test wording and the e2e scenario.

No live Kubernetes gameplay test is part of this plan; the kind step in WU-F is optional and hosted-only.

---

## 9. Dependencies, risks and Definition of Done

**Dependencies [V]:** CR-18 (present: the fence, `WorldDigest`, read-back classification, fault hooks); CR-43 (present: mode guards); `universe.Init` and `PutUniverse` (template); `maxSupport` and `Shipdata` (present); Phase 3b RNG (not present: isolate the RNG behind one function and inject order in tests); `test/ctool` (present) for C fixtures.

**Not dependencies:** CR-17 (section 5.2); identity service (Phase 6/6m); typed API (Phase 4b); TypeScript client (Phase 7).

**Risks [R]:**

1. **Store compatibility.** The additive meta key must be proven harmless to the deployed image; a new commit kind must **not** be introduced (section 5.2). Mitigation: N14 and a note in the deploy runbook that the first CR-07 image cannot be rolled back **after** an enrollment unless the placement key is ignored by the old reader (verify).
2. **Fixture fidelity.** C's rounding, retained metamorph covariances and `blah[8]` read are quirks; fixtures will encode whichever convention is chosen (Q4). Decide first, then generate.
3. **RNG ownership.** Placement randomness must not consume the turn RNG in a way that changes turn outcomes; until Phase 3b, use a separate seeded source for placement or accept that enrollment advances the shared stream (document).
4. **Unauthenticated endpoint.** Opening the web form to the LAN before identity exists exposes slot exhaustion and password reuse; keep it gated, add `gbctl enroll` as the authenticated path, rate-limit the HTTP path (Q8).
5. **Two sources of truth between turns (CR-17).** Commands still write per record; an enrollment installed in memory after a successful transaction is consistent, but a later command that rewrites the planet from a stale copy (the CR-17 pattern) could revert the sector. Mitigation: the install step replaces the in-memory planet so subsequent commands see it; audit the planet-reload call sites (`commands_load.go`, `commands_order.go`) for the home planet case; this is pre-existing CR-17 territory and is documented, not fixed, here.
6. **Governor authentication bug** (`Governors = 1` with empty governor-1 password admitting the race password, section 4.2 i) exists today and is independent of placement; fix it in WU-D and record it as its own finding if the review agrees it is reproducible.
7. **God race.** C-faithful universes have a God race first; Go has none. Decide whether `gbctl enroll --god` (admin only) creates one and whether its placement follows the same rules (C does place it).

**Definition of Done [R]:** WU-A to WU-F merged through reviewed pull requests with hosted CI green; the Phase 3c enrollment acceptance test and the matrix of section 8 pass locally and in CI; `go test -race ./...`, lint baseline unchanged or reduced, offline manifest suite unchanged; the development server can enrol one race through `gbctl enroll` under a separately authorised operation, log in, and run a turn with that race governed; documentation updated; `go-code-review.md` CR-07/CR-08/CR-09 marked closed with evidence; no change to the known-defect inventory needed; CR-43's rolling-update acceptance item 3 remains separately tracked.

---

## 10. Questions requiring owner decisions

1. **Player-number rule:** C append-only `highest+1` with cap 63 (never reuses numbers) or the current lowest-free-slot 1..64? Recommendation: C's rule (`highest+1`, cap 63) to avoid resurrecting a dissolved player's number; `Race.Dissolved` is then the only "gone" marker.
2. **Homeworld window and vacancy:** racegen's `RTEMP ∈ [-200, 100]` with star-level occupancy only, or `enrol`'s ±50 with planet-level vacancy? Recommendation: racegen window **plus** the planet-level vacancy check (strictly safer; the C++ does the same).
3. **Search order:** random without replacement over all stars (racegen) seeded from the engine RNG, with an injectable order for tests. Confirm.
4. **Cost convention:** C-exact (truncating `ROUND`, integer `l_fudge`, metamorph table retaining IQ covariances, define `blah[8]`), or the C++ corrections (`lround`, exact `l_fudge`, clean metamorph table, 600 for eight types)? Recommendation: C-exact for parity fixtures, with `blah[8] = 600` as the only defined extension (C reads garbage there), recorded in the ADR.
5. **Capital fallback:** C's (0,0) regardless of type (can give `maxpopn 0`) or fail with `NO_CAPITAL_SECTOR` and let the player choose another home type? Recommendation: fail.
6. **Governor 0 password:** C racegen fixes "0"; today the race password hash is reused. Recommendation: a separate `governor_password` in the request (default: must be supplied), bcrypt-hashed; never reuse the race hash.
7. **Government ship number:** C `Numships()+1` ignoring the free list; Go allocator pops the free list first. Recommendation: Go allocator inside the transaction (consistent with `build`).
8. **Gate and exposure:** rename `GB_ENROLL_PREVIEW` to `GB_ENROLL_OPEN` once races are playable; keep off by default; add `gbctl enroll` as the authenticated path; rate-limit the HTTP path. Confirm, and decide whether the LAN form may be opened for development at all before identity (Phase 6/6m).
9. **God race:** create one via `gbctl enroll --god` (placed like any race, C-style) or leave Go universes without a God race? C requires it first; nothing in the Go turn code has been shown to require it (**[A]** to verify during WU-D: grep for `God` in turn and command code).
10. **Translate value for others:** C 1 (C++ 0). Recommendation: C.
11. **Request field set:** adopt the full racegen attribute set (including Fighters, Absorb, Pods, Collective IQ, Metamorph, address) as the single request schema for web, `gbctl` and `racegen`. Confirm.
12. **Scope of CR-08/CR-09 closure:** fold the label fix and the "visible without restart" assertion into WU-D/E (recommended) or track separately.

---

## 11. Codex handoff: where to begin

**State of the world when this was written (2026-11-09 UTC):** `master` = `7c5b3921`; the development server (`gb-dev`, StatefulSet `gb-engine`, Pod `gb-engine-0`) runs image `sha256:d62c1fe6…` (CR-18 + CR-43), SERVING at 18 updates / 0 segments with commit record 3, enrollment disabled, no players; evidence in `/home/steve/gb-evidence/cr43-stage4-resume-2026-11-09/` and `docs/testing/cr18-cr43-stage4-observation-2026-11-09.md`. **Nothing in CR-07 may touch that server**: all work is local, in a worktree, with disposable databases.

**Repository rules that apply (from `CLAUDE.md` and the standing constraints):** never modify `source/`; develop in a dedicated worktree (`git worktree add /home/steve/worktrees/GB-cr07 -b feature/cr07-enrollment origin/master`), never in the shared checkout; do not install Docker, Podman, kind or GitHub CLI on the host; do not run kind locally; do not change the known-defect inventory, golden allowances or lint baseline without the owner's acknowledgment labels; protected gate tooling changes need `gate-change-reviewed`; commits end with the attribution line the session reminder prescribes; pull requests are opened by the owner from the compare URL (no `gh` or token on the host) with a prepared description; obtain an independent review before asking for the merge; hosted CI must pass. Local gates to run before every push: `go test ./...`, `go test -race ./internal/game ./internal/db`, `test/scripts/lint-gate.sh`, `deploy/kustomize/tests/run.sh --require-published-image`, `PR_LABELS_JSON='[...]' test/scripts/growth-gate.sh origin/master`, `test/scripts/inventory-check.sh`, `python3 -m unittest discover -s .github/scripts -p 'test_*.py'`, `python3 .github/scripts/workflow_policy_check.py`, `git diff --check`, `gofmt -l`.

**Start here, in order:**

1. Read this document, then `docs/testing/cr18-handoff.md` section 8 (the earlier CR-07 sketch, consistent with this plan but paraphrasing `enrol.c`), `docs/architecture/microservices-migration-plan.md` sections 6.7, 8.4 and Phase 3c (`:458-470, :760-783, :1402-1416`), ADR-0009 and ADR-0010 (the transaction and read-back patterns to copy), `go/internal/game/admin.go:307-403` (`InitUniverse` and today's `Enroll`), `go/internal/game/commit.go:102-228, 269-436`, `go/internal/db/commit.go:134-230, 342-503`, `go/internal/db/meta.go:137-190`.
2. Get the owner's answers to section 10, at least Q1, Q2, Q4, Q5, Q6, Q7 and Q11, before writing fixtures; record them in the WU-F ADR draft from the start.
3. Implement **WU-A** first (pure, no engine risk): move `cmd/racegen` tables into `go/internal/racegen`, generate C fixtures with `test/ctool` (`make -C test/ctool fixtures`, verify with `make -C test/ctool check`; this touches protected tooling), make the CLI use the package. Open the pull request.
4. **WU-C** can proceed in parallel (store method with fault hooks and compatibility test N14).
5. Then **WU-B**, **WU-D**, **WU-E**, **WU-F** as specified, each with its tests from section 8, each reviewed independently.
6. Do not deploy or enrol on `gb-dev` as part of this work; a later, separately authorised operation (publish image, digest pull request, sync by `deploy.md` section 5.1, then one `gbctl enroll`) is the acceptance on the live server.

**Scratch evidence from this audit** (host-local, not in Git): the exported trees used for citations are under the session scratchpad (`cr07/GB` for master `7c5b3921`, `cr07/kaladron` for `b67e4c3b`); they can be recreated with `git archive 7c5b3921 | tar -x` and `git clone https://github.com/kaladron/galactic-bloodshed && git checkout b67e4c3b`. All line numbers in this document were taken from those exports.

**Client direction (unchanged):** the player interface is a TypeScript graphical web client built on the Go-owned API of section 5.4 and the migration plan's section 7; the C++ repository's Python client is a UX reference only and is not a planned deliverable.
