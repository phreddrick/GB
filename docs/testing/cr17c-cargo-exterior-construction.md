# CR-17c atomic Cargo Habitat, Platform and Mirror construction

This [#44](https://github.com/phreddrick/GB/issues/44) slice covers an eligible ordinary Cargo Ship in star orbit building a Habitat (H), Orbital Assault Platform (O), or Space Mirror (M). It accepts the exact type letter with an omitted count or explicit 1 or 2. Other forms, builders, hulls, God builds, and planet or universe orbit retain their existing routes. The three [Station forms](cr17c-cargo-station-construction.md) use the same implementation and keep their existing behavior.

On upstream base 22ba54a839d23cf057ba17ebf84a66a3d35549e1, real dispatch reported success for each of H, O and M while the child was still absent from bbolt. After the command and a cold reopen without a shutdown flush, the child was present but the star still pointed at the old head and Cargo retained its pre-build resources, crew and fuel.

## Compatibility

The legacy Go builder and the original C sources agree on the construction mask and minimum technology for these three hulls, per-child affordability and autoload, damage, repair notices, and star-list placement. The C entry points are [build.c](../../source/build.c) and [shipdata.h](../../source/shipdata.h). C also tests governor authorization in [testship](../../source/getplace.c), and has different grammar and some different refusal text. This slice preserves the current Go race-ownership check, assigns each child to the issuing governor, uses that governor's autoload toggle, and keeps Go's existing messages and routing.

| Hull | Minimum technology | Default ordinary cost | Max crew | Max fuel |
|---|---:|---:|---:|---:|
| Habitat | 100 | 592 | 2,000 | 2,000 |
| Orbital Assault Platform | 200 | 423 | 200 | 2,000 |
| Space Mirror | 100 | 115 | 5 | 20 |

All start 75% damaged, can repair without resources, and use the ordinary design defaults, including the Platform's initial absence of God-only equipment. Cost, crew, fuel, child mass, unchanged builder mass and zero AP follow the legacy Go path. The second child loads from the remainder after the first. A full request for two may still succeed when loading the first emptied the builder's crew. Success is reported in allocation order, with the shortage notice after all success-related notices for an affordable prefix of one. An unaffordable request makes no writes.

## Transaction and validation

The actor checks its complete exterior list against the durable witnesses. The typed plan accepts only Station, Habitat, Platform and Mirror. The existing bbolt transaction checks the race, star, builder and list rows, and independently validates absent, distinct ship IDs, structural references and the full free-list state. It persists the affordable batch, Cargo debits and loads, the star head and consumed IDs together, preserving LIFO reuse and fresh append order. No success is sent before confirmed state is installed in the actor. Valid pending differences defer. Structural corruption, late conflicts or uncertain commits halt without legacy retries or stale shutdown flushes.

Dispatcher tests compare all durable records to an independent legacy-Go oracle and check exact output at the original callback boundary. They exercise both autoload settings, zero/partial/full affordability, fresh/reused/mixed IDs, and an existing exterior sibling ahead of the selected Cargo. Negative tests cover technology thresholds, governor and route behavior, corruption, valid pending differences and late witness changes. The historical detached-list tests call the unchanged legacy producer for these newly migrated forms, then dispatch actual dock and launch commands.

The crash harness kills a separate Go test process at named database and actor boundaries. It reopens a fresh bbolt handle before any turn and checks for the complete old or new logical state. The postcommit Habitat (affordable prefix), Platform (mixed allocation), and Mirror (LIFO reuse) cases also exercise scheduled segment, full update, restart, and numeric dock/undock. This covers process death at known boundaries. Physical I/O faults and power loss are outside this evidence.
