# CR-17C #44 — One Space Probe built in a Carrier’s hangar

From ship scope, an eligible planet-orbiting Carrier builds one Space Probe
with `build :`. The ordinary non-God, autoload-off path uses legacy first-letter
selection and effective-count parsing. The Carrier must be alive, active,
undocked, crewed, undamaged, sufficiently advanced and owned by the caller.
The leader can use any owned Carrier. A subgovernor must be active and assigned
to the builder. The bounded command also refuses dissolved races and inactive
leaders. Legacy construction did not enforce all of those authority checks.

The probe costs 24 resources, occupies one hangar unit and costs zero AP. It
enters at the head of the hold with zero crew and fuel. The Carrier retains
its previous children, mass, fuel and crew. Other child types, orbits, God
builds, autoload and multiple builds retain their existing handlers.

## Atomic construction

The command takes private ship proposals and snapshots of the race, planet,
star (including AP), complete exterior orbit list and immediate hold. The closed
bbolt operation recomputes the probe and debits, checks all persisted preimages
and revalidates the allocation ticket in the writer. It creates the child,
updates the Carrier’s resource balance, Hanger and Ships head, and consumes
the precise free-list key on reuse. Append requires an absent ship number.
On confirmed commit, it installs the child and updates the existing Carrier
object before releasing the four build notices.

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
fail. The 18 real SIGKILL cases cover **planet-orbit probe construction**,
with nine boundaries on each allocation path. They verify old-or-new logical
worlds, including the allocator, after reopening disposable stores. Navigation
crashes, physical disk failure, and broader #44 construction remain outside
this coverage.
