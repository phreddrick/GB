# CR-17C #44 — One Carrier built by a cargo ship

In ship scope, an eligible cargo ship can build one Carrier with `build X`.
The existing first-letter selection and effective-count rules apply. The new
Carrier enters the builder's planet, star or universe orbit as the list head.
Multiple builds, other child types and other builder types keep their existing
handlers. The separate planet-built factory case has its own transaction.

## Cost and authority

An ordinary Carrier costs 354 resources and zero AP. With autoload on, the
child receives up to its crew and fuel capacities from the builder. With
autoload off, the builder pays only resources. God builds retain their free
design and full starting load. The builder's mass and hangar are unchanged.

The builder must be alive, active, undocked, crewed, undamaged and owned by
the authenticated player. The bounded path also checks that the race is active
and the governor is active. The leader can operate any owned cargo ship.
A subgovernor must be assigned to the builder. Legacy ship construction
checked the owner without enforcing that assignment.

## Persistence and failure behavior

On the original path the child could survive restart while the builder
recovered its spent resources, crew and fuel. The closed bbolt transaction
writes the child, builder debits, orbit head and any consumed free-list entry
together. It checks the allocation ticket and full preimages of the race,
builder, orbit root and chain ships, plus the star for a planet build. Append
and free-list reuse follow the planet-factory allocation rules.

A valid pending legacy save defers construction. The player can retry after
the next persisted turn. A late preimage conflict aborts and halts the engine
before installation or success output. Uncertain persistence sets the shared
write guard and prevents stale shutdown flushing. On confirmed commit, the
actor installs the child, builder debits and orbit link before releasing build
notices. A subsequent reporting failure leaves the commit durable.

## Regression coverage

`TestCargoBuildDispatch` exercises all three orbit roots with append and reuse.
`TestCargoBuildLateWitnessWriter` changes only the stored race or a linked
non-builder ship between admission and transaction, then checks the full
database against the external write alone. The 18 real SIGKILL cases cover
planet orbit, both allocation strategies and nine boundaries per strategy.
Star and universe orbits have dispatch coverage. The planet-orbit scheduler
test follows exact debits and chain membership through a movement segment,
full update and shutdown/reopen.
