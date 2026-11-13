# Independent Phase 0 runtime-evidence revalidation

Baseline inspected: `7a17a1bc7edf75091386f30944a529a0ccf48d8d`. This supplements `independent-phase0-review.md`; it does not revise historical evidence or approve a production implementation.

## Review verdict

**Phase 0 evidence is independently reproducible and supports the ship-retreat mass blocker.** Current actor dispatch safely refuses hostile c/m unloading. The unmodified C helper can remove mass even when every attacker returns, including negative mass from a physically valid initial crew/mass fixture. Existing owner-approved carrier treatment does not explain this defect. An explicit ship-specific survivor-mass disposition remains necessary under the task's approval boundary. No architecture/schema blocker is inferred.

## Fresh independent executions

I rebuilt the C harness into a separate binary using the parent's recorded successful invocation, changing only the output filename:

```
gcc -std=gnu89 -fcommon -w -O0 -ffunction-sections -fdata-sections -Wl,--gc-sections -Isource -Itest/ctool/include -I/tmp/cr17c-ship-assault-evidence /tmp/cr17c-ship-assault-evidence/c-ship-audit.c /tmp/cr17c-ship-assault-evidence/c-unreached-stubs.c source/load.c source/move.c source/rand.c -lm -o /tmp/cr17c-ship-assault-evidence/independent-c-ship-audit
```

Build exit0; stdout/stderr empty. `source/load.c`, `source/move.c` and `source/rand.c` each match the baseline Git objects byte-for-byte. Each of the six helper definitions in `c-helpers.inc` matches its original source definition after whitespace normalization. The harness overrides only the underlying random tape and reporting/storage adapters for deterministic isolated evaluation. Unreached function stubs abort99, so any reached stand-in fails rather than supplies invented gameplay. The preserved initial linker failure was harness setup, not a production test failure; duplicate existing use_fuel/use_destruct definitions were removed and unreachable mount references supplied with aborting stubs before the recorded successful rerun.

All five independently rebuilt executions exited0 with empty stderr and output byte-for-byte identical to the parent's corresponding `.out` files:

| C case | Ship result | Sector owner/civ/mil | AP | Draws | Interpretation |
|---|---|---|---|---|---|
| zero-loss-retreat | mass210→−190; crew100→100 | 2/100/0 | 100 | 6 | No casualties; mass still decreases400. Initial race mass2 and crew100 leave valid base mass10. |
| partial-retreat | mass700→400; crew200→150 | 2/50/0 | 100 | 6 | Selected ship lost50 civilians, but C debits300 mass rather than casualty mass100. |
| military-retreat | mass700→400; troops200→150 | 2/0/50 | 100 | 6 | Same incorrect double-debit for troops. |
| victory | mass700→500; crew200→100 | 1/100/0 | 100 | 4 | Entire departing100 remains off ship; mass200 debit is coherent. |
| mutual | mass302→300; crew1→0 | 1/0/0 | 100 | 5 | C wrongly reports attacker victory/owns an empty sector; already superseded by approved no-winner neutral policy. |

The C harness directly calls `unload_onto_alien_sector` and actual `ground_attack`/`rand.c`; it does **not** execute C command parsing, ship membership, AP admission or bbolt persistence. Helper AP remains100; the zero-cost dispatcher and early-return conclusion is additionally supported by source inspection, not claimed as C full-dispatch execution. Fixed random tapes make the five outcomes deterministic; this is not a statistical distribution test, absorption coverage or an implementation acceptance test.

I separately reran the actual Go actor refusal overlay:

```
GOTOOLCHAIN=local GOCACHE=/tmp/cr17c-habitat-cache /tmp/gb-go1274-evidence/go/bin/go test -count=1 -race -json -overlay=/tmp/cr17c-ship-assault-evidence/overlay.json ./internal/game -run '^TestShipAssaultPhase0Refusal$'
```

Toolchain reports Go1.27.4 linux/amd64. Exit0, eight passed leaf cases, zero failed nodes, no stderr or race report. Fresh game-package elapsed3.792s; end-to-end command wall9.984s. Cases cover civilians/troops × explicit100/omitted/zero/negative1. Each reaches the actual actor command dispatcher, requires the deferred refusal, compares pre/post actor world snapshots and compares all durable records and bucket sequences. The stored snapshot contains34 evidence entries per fixture. The actor snapshot covers stars, planets, races, ships, star data, directories, power and block indexes; it does not enumerate every administrative State field (for example volatile groundAssaults). Describe it as complete **world/index** comparison, not all State fields. This is focused refusal evidence, not a fresh full suite or crash execution by this reviewer.

## Evidence and accuracy notes

The parent's original C `.result.json`, `.out`, `.err` records agree with these reruns. The initial failed C link logs remain preserved. The parent's accepted-regressions JSON is execution evidence from the parent, not independently rerun here; do not conflate that with this review's fresh commands.

Independent supporting files: `independent-runtime-results.json`, `independent-c-build.out/.err`, `independent-<case>.out/.err`, `independent-helper-comparison.json`, `independent-source-check.json`, `independent-refusal-result.json`, `independent-refusal.jsonl/.err`. No original artifact or repository file was modified.

Selected SHA-256 identities:

- C harness: `f2c9e8ebe3b683bcd4d0d7fce03f3ff3de289cb150bd84b15a6c8e6514836a60`
- Helper extraction: `69b8e4a8ac56671529992e71330656743e0b7a54bc88e0bcee1229e6637baec9`
- Unreached stubs: `0948b373f64e432eda9e14c422998e27c13179ba8d8331bb30073795e6265e12`
- Refusal overlay source: `2b398e14f3adb82c779f76d1aa40dce56097be0eae7f529f034df0942e35b3eb`
- Independent C runtime ledger: `1c8535feeeb612052d683e47e81498ccb4617b28f582530a1779c756f7645728`
- Independent helper comparison: `c5c2fc240f4477b64737228d321698a09ee2d314d7a702afea96c24934ac17c9`
- Independent source comparison: `a524f02c1e5d494f1e23dd6872ac1d6e0f0158dd8df4d08aadde3888d68a22e5`
- Independent Go command/result: `6dda996a86c07535d9eb042d3e7596d16072478cbd0dd7a74a78ca54f2d59e9d`
- Independent Go JSONL: `0f9f882250acde748a4db67eca94c916252f024b6002017799d07524122852bb`

No implementation approval, full gameplay acceptance or live-system operation is claimed.

## Parent Phase 0 implementation-record review

Read the newly drafted `docs/testing/cr17c-ship-ground-assault-phase0.md`. Its mass formulas, conditional participants, C/helper-versus-dispatch distinction, safe refusal scope, explicit implementation stop and historical/remaining-validation limitations accurately reflect the inspected evidence. No blocking overclaim or additional gameplay decision is introduced by the document. The explicit-zero/VN default wording records a boundary to resolve in implementation rather than declaring an unapproved new rule.

Independently parsed (not rerun) the parent's accepted-regressions JSONL: 465 passed test nodes,437 leaves,0 failed nodes; commandrules1.093s,db6.779s,game22.622s. There are44 real-process crash leaves (24population+20ground), plus two ordinary crash-child test nodes; do not count those two children as extra SIGKILL scenarios. The recorded focused run uses `-run`, so inventory validation explicitly reports not executed; the document correctly defers full inventory/gates until implementation. The commands ledger now records the initial failed C build and corrected build without losing either history.
