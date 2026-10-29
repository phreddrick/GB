# ADR-0001: Modular monolith before service extraction; extraction conditional on gates

Status: Accepted 2026-10-28 (planning decision approved with migration plan revision 2.1; Phase 0 implemented under it)

## Context

The Go port (`go/`) is a single binary whose `game` package depends on the transport package, holds the whole world in memory with three different persistence rules, and has no tests (`go-code-review.md` sections 2, 3, 5 and 6). The owner's goal is a Kubernetes-deployed, GitOps-managed game with a browser GUI, possibly split into services (engine, gateway, identity, optional journal). Galactic Bloodshed's turn is one atomic pass over the whole world, so the simulation itself cannot be split (plan section 3.4, alternative D).

## Decision

1. The simulation stays one authoritative engine per universe, inside one consistency boundary (one commit transaction).
2. Before any service is extracted, the code becomes a **modular monolith**: enforced package boundaries (`rules`, `world`, `worldstore`, `view`, `engine`, `gateway`, `identity`, `platform`), in-process interfaces shaped like the later gRPC contracts, a structured HTTPS/WebSocket API, and the browser proof of concept, all tested (plan Phases 3 and 4).
3. Extraction of the gateway (Phase 5) and identity (Phase 6) is **conditional** on explicit go/no-go gates decided by the owner (plan Decision 1). If the gates are not met the project stops at the modular monolith, and the identity work is done in-process as Phase 6m. Every later phase (7, 8, 9) is written to work on either route.

## Alternatives considered

- Containerise the current monolith and stop: keeps every defect; rejected as an end state, kept as the Phase 2 milestone.
- Extract services immediately: would freeze today's broken boundaries (domain importing transport, dual persistence) into network contracts; rejected.
- Fine-grained services (ships, planets, combat, market): a turn reads and writes all of them in C order; would need a distributed transaction for a workload one core finishes in seconds; rejected.

## Consequences

- Phases 0 to 4 are identical whatever the eventual route; nothing in them is wasted if extraction is declined.
- The plan carries two dependency routes (plan section 16.3) and the Definition of Done names both.
- Boundary tests (`go list -deps` assertions, Phase 4a) are the mechanism that keeps the monolith modular; without them this ADR is only intent.

## Validation

- Phase 4a exit: import-boundary tests green; one binary.
- Phase 5 and 6 may start only after the owner records a go decision against the gate criteria in the plan.

## References

- `docs/architecture/microservices-migration-plan.md` sections 1, 3, 16.3, 22 (Decision 1).
- `docs/architecture/go-code-review.md` sections 2.3, 3.2, 5, 6.
