# CR-17C #44 — One Space Probe built in a Carrier’s hangar

From ship scope, `build :` constructs one Space Probe inside an eligible
planet-orbiting Carrier with autoload on or off. The ordinary non-God path uses
legacy first-letter selection and effective-count parsing. The Carrier must be alive, active,
undocked, crewed, undamaged, sufficiently advanced and owned by the caller.
The leader can use any owned Carrier. A subgovernor must be active and assigned
to the builder. The bounded command also refuses dissolved races and inactive
leaders. Legacy construction did not enforce all of those authority checks.

The probe costs 24 resources, occupies one hangar unit and costs zero AP. It
enters at the head of the hold with zero crew. With autoload off, it receives
zero fuel. With autoload on, it receives the lesser of the Carrier's available
fuel and the probe's 20-unit capacity, including fractional fuel. The Carrier
loses exactly the fuel loaded and retains its previous children, mass and crew.
As in `source/build.c` (`autoload_at_ship` and `initialize_new_ship`), the
probe's mass is its base mass plus loaded fuel times the fuel mass coefficient.
With autoload on, a negative or non-finite Carrier fuel balance halts the engine
before construction writes or success notices. The same safeguard applies if
floating-point subtraction cannot debit the amount the child would receive.
Other child types, orbits, God builds and multiple builds retain their existing
handlers.

## Atomic construction

The command takes private ship proposals and snapshots of the race, planet,
star (including AP), complete exterior orbit list and immediate hold. The closed
bbolt operation recomputes the probe and debits, checks all persisted preimages
and revalidates the allocation ticket in the writer. It creates the child,
updates the Carrier’s resource and fuel balances, Hanger and Ships head, and
consumes the precise free-list key on reuse. Append requires an absent ship number.
On confirmed commit, it installs the child and updates the existing Carrier
object before releasing the four build notices.

## Autoload split save on the PR #90 baseline

On `f4acdc42680f450a4df996eda5f1949b29e82c28`, a real `toggle autoload`
followed by `build :` bypassed the probe transaction. With 29.375 fuel the
actor charged the Carrier 24 resources and 20 fuel. It linked the new probe
and raised occupied space from 4 to 5. The store contained the fueled probe,
but its Carrier still had 500 resources, 29.375 fuel, occupied space 4 and the
old head. Both append and free-ID reuse reproduced the split. The corrected
dispatch uses the existing transaction for either value of the active
governor's toggle. It preserves the legacy actor result and notices.

| Admission or outcome | Observable result |
| --- | --- |
| Valid pending legacy changes, including a volatile scrap AP debit | SERVING. No build. Retry manually after the next persisted turn. |
| Agreed, traversable hold with a historical planet-orbit child, a dead child still linked, noncanonical docking state, or a recorded occupancy different from live child sizes | SERVING. Explicit unsettled-hangar refusal before allocation. The command preserves the entire input world. |
| Invalid identity, wrong structural parent, missing linked record, cycle or a child also reachable in the planet orbit | HALT. Both actor and stored topology are checked, including tails behind a historical child. |
| Late preimage conflict or known transaction failure | Confirmed abort. No construction or success notice. The engine halts for reconciliation. |
| Uncertain commit | HALT with the shared uncertainty guard. No unverified proposal or stale shutdown flush. |
| Confirmed commit with installation or reporting failure | The stored commit stands. HALT suppresses stale flushing. |

Operators will see **ship construction** in HALT reasons from the shared
construction runner. This replaces the earlier **cargo construction** wording
for the existing cargo-ship path as well. Live notices remain best-effort.

## Hangar bookkeeping and older worlds

C sets the child’s orbit, destination level and destination ship number in
`source/GB_server.c:insert_sh_ship`. C land and launch use `Size(s)` in
`source/land.c` and `source/launch.c`. That macro reads `s->size` in
`source/ships.h`, a field computed by `ship_size(s)` during construction in
`source/build.c`. C scrap reads `s->size` directly in `source/scrap.c`. Go
uses the stored `Ship.Size` for all three commands.

In the previous Go implementation, landing inserted a
child without setting its parent fields and charged hangar space twice.
Launching that child could follow the planet-launch path and leave it linked
in the hold. Scrapping a sibling could leave a dead member whose space had
already been released. Either sequence could then halt probe construction.

Both surface loading and flying landings now install the parent fields and
charge the stored size once. A launch moves the child to the Carrier’s exterior
and credits its size once. Non-head removals persist the changed predecessor.
A self-landing or a landing on a target absent from its claimed orbit is
refused. Legacy hangar construction uses the same insertion bookkeeping.
Scrap still spends its normal AP and can leave a dead child linked until the
next turn. These navigation and scrap commands retain their existing separate
writes and crash limitations.

Segment and update rebuilds use the existing orbit fields to place living
ships. During that rebuild a living Carrier’s occupied space is derived from
its rebuilt direct children. An old child still recording planet orbit therefore
reappears in that orbit. An unrepresentable total cannot be narrowed into free
space and remains ineligible for probe construction. This is ordinary turn
processing; the build command neither repairs a saved record nor retries.

## Tests

Actual actor commands cover land, launch, launch of a non-head sibling, scrap
alone and after landing, surface loading, star-orbit landing, legacy interior
construction and one-probe builds on append and free-ID reuse. Historical
snapshots are constructed explicitly. Tests compare complete logical database
records and allocator sequences, actor state, output and mode. They exercise
post-scrap deferral, agreed dead-child refusal, and manual construction after
a scheduled segment, full update and restart.

Late-writer cases change the Carrier, hold child, active governor’s race row,
or another ship in the exterior orbit between admission and the writer.
Removing either the race or exterior-witness comparison makes its regression
fail. Autoload-specific cases change just the governor's toggle or the Carrier's
stored fuel. Dispatcher tests compare zero, fractional, exact-capacity and
excess available fuel on both allocation paths. They check the installed world
at each notice. Scheduled segment, full update and restart checks retain
both ships' fuel and the probe's mass and reachability.

The 36 real SIGKILL cases cover **planet-orbit probe construction**,
with nine boundaries on each allocation path for each toggle setting. They verify
old-or-new logical worlds, including the allocator, after reopening disposable stores. Navigation
crashes, physical disk failure, and broader #44 construction remain outside
this coverage.
