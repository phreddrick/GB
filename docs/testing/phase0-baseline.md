# Phase 0 baseline report

Recorded before any Phase 0 change was made. Date: 2026-10-28. Machine: the owner's workstation (Linux, Go 1.27.3 at `/usr/local/go`).

## Git state at start

| Item | Value |
|---|---|
| Branch | `master` |
| HEAD | `71eaee729f84976cc54a048e04731a5816c40953` ("Updated for deploying to kubernetes") |
| Remote `origin` | `git@github.com:phreddrick/GB.git` |
| `git status --short` | `?? docs/` (the two architecture documents, untracked, produced by the assessment and planning sessions) |
| Pre-existing uncommitted work | only `docs/architecture/go-code-review.md` (409 lines) and `docs/architecture/microservices-migration-plan.md` (1,999 lines, revision 2.1). Both must be preserved and included in the eventual change set. |

## Go entry points

| File | Tracked? | Cause |
|---|---|---|
| `go/cmd/gb-server/main.go` | no (ignored) | `go/.gitignore:2: gb-server` matches the directory name |
| `go/cmd/gb-client/main.go` | no (ignored) | `go/.gitignore:3: gb-client` |
| `go/cmd/makeuniv/main.go` | no (ignored) | `go/.gitignore:4: makeuniv` |
| `go/cmd/racegen/main.go` | no (ignored) | `go/.gitignore:5: racegen` |

`git ls-files go/cmd` lists nothing. The root `.gitignore` also contains bare `makeuniv` and `racegen` (for the C binaries built in `source/`); those patterns match `go/cmd/makeuniv` and `go/cmd/racegen` as well, so both files must be fixed.

## Build inputs

- `go/go.mod`: module `github.com/gbgame/gb`, `go 1.25.0`; requires `github.com/golang-jwt/jwt/v5 v5.2.1`, `github.com/gorilla/websocket v1.5.3`, `go.etcd.io/bbolt v1.3.10`; indirect `golang.org/x/crypto v0.50.0`, `x/sys v0.46.0`, `x/term v0.44.0`.
- `Dockerfile` (repository root): `golang:1.25-alpine` builder, `COPY . .`, builds `./cmd/makeuniv`, `./cmd/gb-server`, `./cmd/racegen` from `go/`; runtime `alpine:latest`. It cannot build from a clean clone today because the `cmd` sources are not tracked (CR-01).
- No CI configuration exists (`.github/` absent).
- Build instructions: `go/README.md` (make targets), `CLAUDE.md`, `KUBERNETES.md` (minikube flow).

## Tool availability on this machine

| Tool | Available | Consequence |
|---|---|---|
| `go` 1.27.3 | yes | all Go checks run locally |
| `gofmt`, `go vet` | yes | |
| C compiler driver (`gcc`, `cc`, `clang`, `tcc`) | **no** (only `cpp-13` and the bare `cc1` from `cpp-13` are present; no `as`, no `ld`, no `/usr/lib/gcc/.../13` crt files) | the golden-fixture harness cannot be linked or run with system tools; see "Toolchain used for golden fixtures" in `phase0-validation.md` |
| `docker`, `podman`, `nerdctl` | **no** | Docker build validation is BLOCKED locally; it is covered by the CI workflow only |
| `golangci-lint`, `staticcheck`, `govulncheck` binaries | no | run through `go run <module>@<pinned version>` from the module cache instead (no system install) |
| `make` | yes | |
| `python3` | yes | used only for scripting around the validation, not by the build |
| cgo | disabled (no C compiler) | `go test -race` reports `-race requires cgo` locally; it runs in CI (ubuntu-latest has gcc) |

## Baseline results (before changes)

| Check | Result |
|---|---|
| `go build ./...` | PASS |
| `go vet ./...` | PASS (clean) |
| `go test ./...` | PASS, `[no test files]` for all 10 packages |
| `go test -race ./...` | BLOCKED locally: `-race requires cgo; enable cgo by setting CGO_ENABLED=1` (no C compiler) |
| `go mod verify` | PASS, `all modules verified` |
| `gofmt -l .` | **19 files unformatted** (the review of 2026-10-27 reported 10; the current tree has 19): `cmd/racegen/main.go`, `internal/db/keys.go`, `internal/game/{commands_cs,commands_fire,commands_load,commands_move,commands_nav,commands_order,commands_power,commands_teleg,planet,ship,state,turn}.go`, `internal/model/{planet,power,race,ship,star}.go` |
| `go run golang.org/x/vuln/cmd/govulncheck@v1.9.0 ./...` | **1 reachable vulnerability**: GO-2025-3553 in `github.com/golang-jwt/jwt/v5@v5.2.1`, reached from `internal/net/auth.go:41` (`parseToken` → `jwt.ParseWithClaims`); fixed in v5.2.2; 17 further vulnerabilities in required modules are not reached |
| `go run honnef.co/go/tools/cmd/staticcheck@v0.8.2 ./...` | **22 findings** (recorded verbatim in `lint-baseline.md`): U1000 unused ×14, S1009 nil-check ×6, SA4006 ×2, SA4017 ×1 |
| `go list -m -u all` | updates available: jwt v5.2.1→v5.3.1, bbolt v1.3.10→v1.5.0, x/crypto v0.50.0→v0.58.0, x/sys→v0.49.0, x/term→v0.47.0, plus transitive test modules (testify, x/net, x/sync, x/text, gofail) |

## Files expected to change in Phase 0

Tracking and formatting: `go/.gitignore`, `.gitignore`, the four `go/cmd/*/main.go` (newly tracked), the 19 unformatted files (whitespace only).
CI and lint baseline: `.github/workflows/ci.yml`, `go/.golangci.yml`, `docs/testing/lint-baseline.md`.
Dependencies: `go/go.mod`, `go/go.sum`, `docs/testing/dependencies-2026-10.md`.
Golden tests and inventory: `test/ctool/**`, `test/golden/testdata/*.csv`, `test/knowndefects.yaml`, `go/internal/testutil/**`, `go/internal/game/golden_test.go`, `docs/testing/known-defects.md`.
Documentation: `go/reference/*.md` moved to `docs/archive/earlier-model-notes/`, `go/README.md`, `CLAUDE.md`, `KUBERNETES.md` (superseded notice only), `docs/architecture/adr/*`.
Module path (Decision 15, provisionally approved): `go/go.mod` and every Go import of `github.com/gbgame/gb` (26 files). The `Dockerfile` does not reference the module path.

Nothing under `source/`, `deploy/` (does not exist), or `~/k8s-helpers` changes.
