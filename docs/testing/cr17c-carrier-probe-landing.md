# CR-17C #43 — Landing one flying Space Probe on a Carrier

A single numeric probe selection and a ship target, e.g. `land #78 #77`,
enter the landing transaction when the living probe and living Carrier are
undocked and declare the same planet orbit. The command uses their physical
orbit in any caller directory. Surface landings, star-orbit and cross-planet
landings, other ship types and list selectors retain their handlers. In
particular the legacy flying-ship proximity rule permits docking from two
different planet orbits. The existing cross-planet dispatcher test covers it.

## Authority and gameplay

Original C `source/land.c:land` checks the governor on the selected ship;
`source/getplace.c:testship` checks ownership, governor authority, liveness
and activity on the target. Legacy Go's numeric selector checked ownership
and liveness; its target helper omitted the governor check. This writer
requires an active governor of a non-dissolved race, owning both ships.
A non-leader must be assigned to both. The Carrier must be active. Launch's
permission to leave a foreign Carrier does not grant permission to land on it.

The overload and distance limits and fuel requirement follow flying-ship
landing. The fuel burn is `0.05 + distance * 0.025 * sqrt(probe mass)`,
using the mass before landing. The probe loses that fuel and its mass
equivalent. The Carrier gains the probe's post-burn mass and its stored
`Size` once in `Hanger` (the Go convention established in PR #90).
The probe retains its coordinates on insertion. It is docked, has ship
orbit and destination, names the Carrier in `DestShipNo`, and becomes
the first child ahead of the previous hold head. This costs zero AP.
Unrepresentable mass arithmetic cannot produce a successful landing.

## Durability

The planner checks both complete lists, planet and star identities, capacity,
authority and ship preimages. The storage entry point derives the changes
again and compares the complete planet, star, race and every ship in both
lists inside the fenced bbolt write transaction. Only the two ships and
the changed exterior link are written. For a head unlink that link belongs
to the planet; for a non-head unlink it belongs to the preceding ship.
When the Carrier precedes the probe, its one record contains the exterior
splice, new hold head, occupied space and mass. The unused planet root,
AP, allocators and schedule retain their values.

A valid pending actor/store difference gives a manual retry hint. An
agreed historical hold or inconsistent occupied-space accounting refuses
the landing while SERVING. A historical planet-orbit declaration alone
does not establish that the ship is flying. Cycles, unexplained missing
members, broken stored links and overlapping exterior/hold chains halt
for reconciliation. No repair or automatic retry occurs. Late conflicts
abort. Uncertain commits halt without installing the proposal or flushing
stale actor objects. Confirmed records are installed into the existing
objects before sending the fuel-use notice.

## Verification

A disposable base-world dispatcher run exposed the split in the old writer.
Immediately after saving the exterior predecessor, neither list reached the
probe; the probe still declared planet orbit. The same invocation subsequently
completed and saved the new hold head.

`TestCR17ProbeLanding` uses real build, launch and landing dispatch, exact
record comparisons, and fault injection. Coverage includes all three splice
layouts, fuel and mass at nonzero distance, both governor assignments, legacy
and pending states, stale witnesses, uncertainty and output ordering. The
real-process cases kill the server before/after commit, after each participant
write, and on either side of actor installation. They compare the whole logical
store immediately upon reopen. A separate scheduled segment, update, shutdown
and restart test checks that the probe stays reachable only inside the Carrier.
