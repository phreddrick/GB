# CR-17C friendly star-orbit docking (#43)

An explicit numeric `dock #A #B` establishes a reciprocal dock between two living ships owned by the caller in the same star exterior. The command sets only `Docked`, `WhatDest` and `DestShipNo` on each ship. The original success message follows durable commit and installation into both original actor objects. Docking costs zero AP and fuel and preserves mass, coordinates, hyperdrive, notification flags, ship numbers and list topology.

## Dispatch and authority

The handler uses the existing command dispatcher and numeric source selection. A bare positive decimal, leading zeroes, and the previously accepted positive sign (e.g. `#+77` or target `+78`) identify the same ship. Existing extra arguments remain ignored. A number that narrows or wraps to an otherwise eligible row cannot bypass the atomic writer. A caller must own the source and target. The session must remain valid for its race and governor. Numeric Go docking allows ships assigned to another governor and inactive vessels. The distance check includes the configured limit.

Self-docking and docking to an occupied target now refuse before changing either ship. A docked source still refuses. A navigation order or an incoming stale claim to a free ship does not reserve it. Existing one-sided claims and self-links can still be released by the explicit star undock command. A historically held ship declaring this star but absent from its exterior can receive a safe refusal when its physical hold accounts for that absence.

Type and wildcard selections (even one match), foreign assault/capture, different valid stars and other orbit levels retain their legacy handling. Legacy docking checks scope level and distance and can still create stale links outside this slice. Hangar landing, launch, scrap, destruction and turn rebuilding have their own semantics. This change does not migrate those writers.

## Compatibility with C

Both Go and friendly C dock use a distance limit and zero AP. C rejects self and occupied targets (`source/dock.c:108–144`). The bounded Go command retains Go's numeric ownership rule. C also tests the source governor, the target governor and target activity (`source/dock.c:59–68,120`, `source/getplace.c:256–275`). C calculates a fuel cost from source mass and distance, burns passenger-independent fuel mass, randomly offsets position, switches off hyperdrive and clears both notification flags (`source/dock.c:148–176,253–260,461`, `source/load.c:874–878`). Those maneuver and authorization differences remain separate gameplay work.

## Persistence and recovery

The closed planner validates caller identity, the star root, the complete ordered exterior (including dead siblings awaiting rebuild), both selected identities and their preimages. The store derives both permitted edits again and compares the entire race, star and each ship record under the bbolt write lock. No orbital or AP record needs a write. Ordinary pending actor/store differences defer for manual retry at a persisted boundary. Structural contradictions, late compare failures, lost installation and commit uncertainty halt and suppress stale shutdown flushes. The existing write fence and durable maintenance guard apply.

The dispatch tests compare complete rows and existing actor pointers at the success notice and the whole logical database after completion. The crash harness issues the real dock and signals the child with SIGKILL before admission to the transaction, after validation, at either row write, before/after commit and on either side of actor installation. Reopening must yield exactly the pre-dock database or the complete pair. Scheduled segment, update and restart tests verify exterior reachability and subsequent undocking. The #96 recovery tests seed the historical occupied-target overwrite and self-link records and still exercise undocking through dispatch.
