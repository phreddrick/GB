# Dependency review, 2026-10-28 (Phase 0)

Run from `go/` with Go 1.27.3. Tools were run through `go run <module>@<version>` from the module cache; nothing was installed system-wide.

## Direct and indirect dependencies before Phase 0

| Module | Version | Latest | Role |
|---|---|---|---|
| `github.com/golang-jwt/jwt/v5` | v5.2.1 | v5.3.1 | session tokens (`internal/net/auth.go`) |
| `github.com/gorilla/websocket` | v1.5.3 | v1.5.3 | WebSocket transport |
| `go.etcd.io/bbolt` | v1.3.10 | v1.5.0 | world database |
| `golang.org/x/crypto` | v0.50.0 (listed indirect, used directly for bcrypt) | v0.58.0 | password hashing |
| `golang.org/x/term` | v0.44.0 (listed indirect, used directly by `racegen`) | v0.47.0 | terminal input |
| `golang.org/x/sys` | v0.46.0 (indirect) | v0.49.0 | |
| test-only modules of dependencies (`testify` v1.8.1, `go-spew`, `go-difflib`, `gofail`, `x/net` v0.52.0, `x/sync` v0.5.0, `x/text` v0.36.0) | various | updates available | not in the build graph of any GB binary |

`go mod verify`: all modules verified.

## Vulnerabilities (`govulncheck` v1.9.0)

Before: **1 reachable vulnerability**, GO-2025-3553 ("Excessive memory allocation during header parsing in github.com/golang-jwt/jwt"), `github.com/golang-jwt/jwt/v5@v5.2.1`, reached from `internal/net/auth.go:41` (`parseToken` → `jwt.ParseWithClaims` → `Parser.ParseUnverified`). Fixed in v5.2.2. 17 further vulnerabilities exist in required modules but no GB code reaches them.

After the change below: **0 reachable vulnerabilities**; the same 17 unreachable ones remain in required modules.

## Changes made

- `github.com/golang-jwt/jwt/v5` v5.2.1 → **v5.3.1** (`go get github.com/golang-jwt/jwt/v5@v5.3.1 && go mod tidy`).
- `go mod tidy` also moved `golang.org/x/crypto` and `golang.org/x/term` from the `// indirect` block to the direct block (the code imports them directly; versions unchanged) and dropped two stale `golang.org/x/sys` checksum lines (v0.4.0, v0.43.0) from `go.sum`. No other module version changed.
- Build, vet and tests unchanged after the bump.

## Deferred upgrades (not done in Phase 0)

| Module | Why deferred |
|---|---|
| `go.etcd.io/bbolt` v1.3.10 → v1.5.0 | the persistence layer is rewritten in Phase 3a; upgrade there, under the crash-injection tests |
| `golang.org/x/crypto`, `x/term`, `x/sys` | no reachable vulnerability; bump together in Phase 1 with the dependency-update automation (Renovate or Dependabot, plan section 11.2) |
| `github.com/gorilla/websocket` | current; the plan's gateway work (Phase 4b/5) decides whether to keep it |

## Go toolchain: minimum version versus build toolchain

Two different things are pinned, deliberately:

| Setting | Value | Meaning |
|---|---|---|
| `go.mod` `go` directive | `1.25.0` | the minimum language version the module requires; it does not choose the compiler and is left where the earlier code set it |
| `go.mod` `toolchain` directive | `go1.27.3` | the default toolchain the `go` command selects when the local one is older (with `GOTOOLCHAIN=auto`), so developers build with the reviewed, patched toolchain |
| CI (`.github/workflows/ci.yml`) | `actions/setup-go` with `go-version: 1.27.3`, `GOTOOLCHAIN=local`, and a step that asserts `go version` prints `go1.27.3` | build, `go vet`, tests, race tests **and** `govulncheck` all run on the same standard library; `GOTOOLCHAIN=local` forbids silent toolchain switching inside the job |
| `Dockerfile` builder stage | `golang:1.27.3-alpine@sha256:d200d2dd76b03a99b52651fd1325aff7dcd35422c35c01bac56571b0507f3deb`, `ENV GOTOOLCHAIN=local`, and a `RUN` assertion that `go version` prints `go1.27.3` | exact patch tag plus the multi-architecture index digest reported by Docker Hub's registry on 2026-10-28 (`HEAD /v2/library/golang/manifests/1.27.3-alpine`, `Docker-Content-Digest`); the image build itself is unverified locally (no container runtime on igor) and is validated by the CI `docker` job. The runtime stage (`alpine:latest`) is unchanged; Phase 2 replaces the whole file with distroless images |

Why this matters: the independent review found that selecting Go from `go.mod` (1.25.0) in CI made `govulncheck` report 37 reachable **standard-library** vulnerabilities, while the local run with Go 1.27.3 reported none. The standard library is part of what is scanned, so the scan is only meaningful for the toolchain the binaries are built with. Note also that `golang.org/x/vuln` v1.9.0 refuses to run on Go 1.25.0 (`requires go >= 1.26.0`), so an unpinned job would have auto-downloaded a newer toolchain just for the scan and reported results for a standard library the build did not use.

Verified locally on 2026-10-28: `GOTOOLCHAIN=local go version` is `go1.27.3`; `govulncheck@v1.9.0` under it reports 0 reachable vulnerabilities; under `GOTOOLCHAIN=go1.25.0` the same command refuses to run (toolchain too old). The 37-finding figure is the independent reviewer's measurement with an older scanner against the 1.25.0 standard library and was not reproduced here.

## Compatibility and build concerns

- Local Go is 1.27.3 (`/usr/local/go`); CI pins the same version (table above).
- The module path was corrected to `github.com/phreddrick/GB/go` (Decision 15; see `phase0-implementation-log.md` for why not the literal `github.com/phreddrick/gb`). No external module imports this one, so the rename has no consumers to break.
- Tooling versions pinned for repeatability: `golang.org/x/vuln/cmd/govulncheck@v1.9.0`, `golangci-lint` v2.15.0, `honnef.co/go/tools/cmd/staticcheck@v0.8.2` (baseline cross-check), `actionlint@v1.7.7` (workflow validation).

A clean vulnerability scan is not evidence that the application is secure: the review's security findings (default JWT secret accepted, tokens in query strings, no origin check, unauthenticated enrollment and status endpoints, no read limits) are all still open and scheduled for Phases 1 and 4b.

## Phase 1 update (2026-10-29)

`go mod tidy` removed `golang.org/x/term` from `go.mod`/`go.sum`: its only user was the interactive terminal check in the old `cmd/makeuniv`, which Phase 1 replaced by a non-interactive wrapper around `internal/universe`. No module was added: the metrics endpoint is a dependency-free text-exposition registry (`internal/platform/metrics`), logging uses `log/slog`, and the admin interface is plain `net/http`. `govulncheck` v1.9.0 on the Phase 1 tree: 0 reachable vulnerabilities (17 in required modules, none called). The `x/crypto`/`x/sys` bumps and the dependency-update automation remain open.
