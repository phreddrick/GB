# CR-17C #43 — Launching one Space Probe from planet orbit

`launch #N` on a living Space Probe in the settled hold of an undocked,
planet-orbiting Carrier moves the probe to the head of that planet's ship
list. The numeric selector also accepts the existing bare ship number, from
any directory. The existing dispatcher uses the first argument. Wildcard and
type selections, other ship types, surface launches, peer undocking, and
Carriers in other scopes retain their existing handlers.

The owner must control the **probe**. The race leader or the probe's assigned
active governor may launch it; the Carrier can belong to a different player.
Original C checks the selected ship in `source/launch.c:launch`. The previous
Go numeric selector checked only ownership and liveness. The physical hold
and exterior orbit determine eligibility independently of builder privileges,
autoload, and the current directory.

## Gameplay and transaction

The probe retains fuel, mass, crew and orders except for the same launch
fields changed by the existing handler. It adopts the Carrier's position and
planet orbit, becomes undocked, and sets its destination level to universe.
The old destination ship number can remain as an inactive field. The Carrier
loses exactly the probe's stored size from occupied hangar space and its mass
from the Carrier's mass. This case costs zero AP and zero fuel. An insufficient
nonnegative Carrier mass balance refuses the launch without inventing mass or
persisting a negative balance.

The command captures the actor directory, caller race and governor, star,
planet, complete exterior orbit list, and complete immediate hold. The planner
validates both lists, disjoint membership and the selected probe's authority.
A single bbolt writer independently derives the result and compares every
participating persistent record with its captured preimage. Its writes are
the planet, Carrier, probe and, when unlinking a non-head probe, its immediate
predecessor. The star, race, unchanged siblings and other orbiting ships are
read-only witnesses. The allocator, free list, AP, schedule and turn metadata
have no writes. Both legacy success lines are released only after confirmed
commit and installation into existing actor objects.

Classification checks hold membership before interpreting the probe's orbit
fields. A traversable historical surface-orbit child, a dead linked sibling,
noncanonical docking or stale occupancy can cause an unsettled-hangar refusal
while SERVING. A destroyed Carrier can remain in the exterior list before a
turn. A segment or update drops it from that list and clears its exterior
link, yet can put its surviving probe back in its hold. Both agreed states
refuse launch while SERVING, after validating the exterior and hold. The
legacy Go command can launch from the wreck. Doing so would extend the
writer to a dead parent whose current planet membership cannot be proved
after the rebuild. Original C `kill_ship` recursively destroys held ships;
Go `killShip` leaves them alive. This slice preserves destruction and turn
behavior. A live parent missing from the agreed exterior, a dangling
post-rebuild link, or membership in both lists is a structural error.

A valid pending actor/store difference defers the command for
manual retry after a persisted turn. Neither outcome changes the world.
Missing links, cyclic or overlapping lists and wrong structural parents halt
for reconciliation. The shared command mutex, fence, durable-maintenance check
and uncertainty guard apply. A late conflict aborts and halts; an uncertain
commit installs nothing and suppresses shutdown flushing. A confirmed commit
survives a later installation or response failure. There is no automatic retry.

## Verification

A disposable real-dispatch `build :` followed by `launch #78` on the parent
revision exposed the planet and Carrier both naming #78 as their head after
the probe write, with the Carrier still charged for five occupied units.
The completed call left four occupied units. Earlier successful navigation
tests established the final state but did not exercise this write boundary.

`TestCR17ProbeLaunch` tests exercise fueled and unfueled builds, head and
non-head unlink, foreign Carrier ownership, governor authority, late writes,
commit uncertainty, and both success notifications. Crash tests kill a real
subprocess at each participant write, on either side of commit and around
actor installation. They compare all logical records and sequence state
immediately on reopen. A scheduled segment, full update and restart separately
exercise unique reachability and occupied space while allowing turn list
reordering. These tests address this launch case only.

The destroyed-Carrier regression dispatches build and scrap, runs a segment or
full update, then dispatches launch. It checks mode, reply, no writer entry,
and unchanged complete actor and durable state. Separate probes distinguish
valid pending parent linkage from structural contradictions.
