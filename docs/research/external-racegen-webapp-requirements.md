# GB: standalone external racegen / player signup web application

**Status:** Owner product requirement captured 2026-11-08 (America/Denver); architecture details and security acceptance remain to be designed. This is **not** an implementation authorization or an accepted ADR.

**Parent requirement:** The Galactic Bloodshed graphical player client is TypeScript-first; see `docs/architecture/microservices-migration-plan.md`, section 0, section 7 and Phase 4c/7.  
**Enrollment reference:** `docs/research/cr07-enrollment-audit-and-handoff.md` (Claude's source audit and Codex's independently reviewed follow-up under PR #22).

## Owner requirement

**Racegen shall be a separate, externally accessible TypeScript web application where prospective players can design a species and sign up to join a Galactic Bloodshed game.** It is distinct from the primary TypeScript gameplay web client.

The web-based race designer should let applicants view the creation-point budget, adjust species traits and environmental preferences, receive immediate server-calculated validation feedback, and submit an enrollment request. Once accepted and successfully placed, the player can enter the separate gameplay application and see the assigned empire, homeworld and government ship.

Do **not** choose or ship the other project's Python curses client as the signup experience; it is only a UX reference. The local Go `racegen` command may remain an offline designer/operator tool, but is **not** the intended public signup surface.

## Logical application boundaries

| Component | Responsibility | Trust boundary |
|---|---|---|
| **Racegen web app (TypeScript)** | Public-facing race-design and signup UI; inputs, explanatory help, point-budget previews, signup/placement status | Untrusted browser; no direct database access, secrets, admin tokens, or privileged engine connection |
| **Identity / enrollment backend (Go)** | Accounts, authenticated signup or controlled pre-registration, enrollment intent and status, anti-abuse controls, request authentication, reconciliation | Holds durable enrollment workflow and controls access to server-side engine placement |
| **Authoritative Go GB engine** | Authoritative race design validation and points, race number, world/homeworld/sector/government-ship placement in an idempotent atomic transaction | Engine owns game rules and single-writer state; only a permitted backend/operator may call placement |
| **GB gameplay web app (TypeScript)** | Login, empire views, maps, player commands and events | Only activated for accounts with a successfully placed/active race |

These are logical boundaries, not a mandate to extract new microservices immediately. Preserve the existing **modular monolith first** decision (ADR-0001). A separate frontend **does not** require a separate database or game-engine microservice. Gate any eventual identity/gateway extraction under the existing migration plan.

## Product workflow to design

1. Player visits the public racegen URL on the Internet (specific hostname and deployment method not yet chosen).
2. Player registers/authenticates, or completes a controlled pre-registration workflow appropriate to the game's current identity stage. No unauthenticated direct engine placement.
3. Player selects a target game/universe if multiple are offered (or the backend assigns the sole allowed game). Prevent requests from crossing game boundaries.
4. Client fetches versioned rules metadata (traits, ranges, homeworld types, sector compatibility, costs and budget).
5. Player edits the race; browser shows a helpful preview, but **authoritative validation and point calculation run in Go**. Do not accept client-provided totals.
6. Signup submits a durable enrollment intent with a unique request id and caller-bound request identity. Backend handles possible duplicate submissions and retries; changed-payload key reuse must be rejected.
7. Backend calls the engine's authorized placement operation only when the game is ready to accept new races. Placement atomically creates the race, homeworld, capital sector, governor and government ship, with a persisted idempotency record.
8. Request status is queryable: `PENDING`, `PLACED`, `ACTIVE`, `FAILED`, and uncertain processing that is explicitly reconciled; **a timeout is not proof of failure**. Follow the existing migration plan's section 6.7 saga semantics.
9. Only after successful placement and account binding can the player enter the main gameplay app.

## Security and operations requirements

- Keep external enrollment **disabled by default** until the owner explicitly authorizes exposure after implementation, independent review, testing and deployment preparation. The current `GB_ENROLL_PREVIEW` / future gate must not silently become public.
- Public HTTPS through a dedicated frontend/gateway route; never publish `gbctl`, admin listener, raw bbolt volume, or direct privileged engine APIs.
- Validate and enforce budgets, boundaries, role permissions, race-slot caps and homeworld availability on the server, including atomic/idempotent retries and no acknowledged partial placements.
- Correct the independently reproduced existing bcrypt error fallback and inactive-governor authentication bug **before** external enrollment is enabled. Never store plaintext credentials when hashing fails, or echo/log secrets.
- Require appropriate identity and session controls; apply rate limits, body limits, anti-automation/abuse defenses, safe error messages, and account/game-level quotas. CAPTCHA, invitations, email verification and manual approval are possible policies to decide later, **not** assumed implemented.
- Handle user information minimally; email/contact fields and optional physical address must be explicitly justified. Physical mailing address is **not required** by the race-design schema by default.
- Enforce service-to-service authentication and game-level authorization; never rely on Kubernetes NetworkPolicy alone for authentication.
- Public signup is a separately authorized security/deployment change. No Kubernetes, ingress, DNS, Secret, TLS, or active-enrollment modification is authorized by this document.

## Scope of the initial CR-07 work

**WU-A (approved for planning; requires its own coding authorization):** shared, pure Go race-design rules, cost/budget validation, and deterministic original-C fixtures. Design its types and rules metadata to be reusable by the eventual public TypeScript racegen client, without implementing HTTP routes, browser code, external exposure, identity, database writes, or credential storage in WU-A.

**WU-B–F:** subsequent placement, atomic store, engine actor, authenticated API and integration/e2e implementation under their own review/approvals. The public TypeScript app and Internet exposure are **additional design and delivery work**, not automatically authorized by the legacy WU-E web-form work unit.

**Later:** structured Go-owned OpenAPI contract with generated TypeScript types, signup form and point-budget feedback, accessibility and mobile-friendly input, clear status/retry UX, safe link to the main game client. The specific web frontend framework, domain name, hosting topology, account approval policy and registration/open/closed modes are future decisions.

## Acceptance criteria for the future external signup application

- Separate browser-accessible TypeScript racegen UI and separate TypeScript gameplay UI, sharing coherent authentication and the Go-owned API contract.
- Public designer validates inputs and displays **server-authoritative** costs and remaining points without writing to game state until signup.
- Completed signup is idempotent and results in exactly one playable race with correct homeworld, owned sector, governor and government ship; uncertain outcomes reconcile safely.
- Simultaneous and malicious signup attempts cannot reuse accounts/slots, overrun capacity, bypass role checks or leak other players' data.
- No plaintext password fallback, inactive-governor authentication bypass, or direct public access to privileged Go admin endpoints.
- Identity, credential binding, throttling, and audit requirements are tested in a disposable environment before any publicly reachable release.
- Opening external signup, domain/DNS/TLS setup, and any Kubernetes rollout require **separate explicit owner authorization**.

## Open design decisions (not blockers for WU-A)

1. Public signups versus invitation-only or owner approval to join a particular game.
2. Account-registration and email verification approach before the planned identity phase is ready.
3. Whether registration chooses among multiple games/universes.
4. Public URL, UI framework, deployment ownership and connection to existing gameplay web frontend.
5. Rate-limit, bot-defense, abuse-reporting and privacy policy choices for public availability.
6. Whether species can be saved as drafts before placement; how draft revisions and retries are keyed.

No existing source, service deployment or acceptance status is changed by recording this requirement.
