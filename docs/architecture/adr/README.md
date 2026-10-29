# Architecture decision records

One file per decision, numbered, never rewritten after acceptance (a later ADR supersedes it). Format:

```
# ADR-NNNN: Title
Status: Proposed | Accepted (date) | Superseded by ADR-MMMM
## Context
## Decision
## Alternatives considered
## Consequences
## Validation (how we know the decision is being followed)
## References
```

The migration plan (`../microservices-migration-plan.md`, section 19) lists the ADRs each phase must write. ADRs record decisions the owner has accepted; recommendations that are still open live in the plan's section 22 as decisions for owner review and are not ADRs.

| ADR | Title | Status |
|---|---|---|
| [0001](0001-modular-monolith-first.md) | Modular monolith before service extraction; extraction conditional on gates | Accepted 2026-10-28 |
| [0002](0002-golden-test-method.md) | C-to-Go golden-test method for parity | Accepted 2026-10-28 |
| [0003](0003-known-defect-inventory.md) | Known-defect inventory and parity-gap reporting; lint baseline | Accepted 2026-10-28 |
