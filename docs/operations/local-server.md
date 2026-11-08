# Running the Go server locally (Phase 1)

This is the operator guide for the Phase 1 server: configuration, secrets,
the admin tool, universe initialisation, health endpoints, shutdown and the
limits that still apply. The game itself is **not playable** (see
`docs/architecture/go-code-review.md`); Phase 1 makes the process safe to run,
observe, administer and restart.

## 1. Build

```sh
cd go
make all            # gb-server gb-client gbctl makeuniv racegen
```

## 2. Configuration

Precedence, lowest to highest: built-in defaults, the JSON file named by
`-config` (or `GB_CONFIG`), the environment. The configuration is validated
**before** the database is opened; an invalid configuration exits with status
1 and nothing is created.

| Variable | Default | Meaning |
|---|---|---|
| `GB_ADDR` | `localhost:8080` | player listener (HTTP login, WebSocket, `/status`, `/livez`, `/readyz`, enrollment) |
| `GB_ADMIN_ADDR` | `127.0.0.1:8081` | admin listener (token-protected operations, `/metrics`, probes). Set to the empty string to disable it; then updates run only on schedule and the universe cannot be initialised through the server. |
| `GB_DB_PATH` | `./gb.db` | bbolt database file. `GB_DATA_PATH` (the old manifest's variable) is accepted as a deprecated alias with a warning; a directory value gets `/gb.db` appended. |
| `GB_JWT_SECRET` / `GB_JWT_SECRET_FILE` | none | **required.** At least 32 bytes, not a placeholder (`change-me-in-production`, `secret`, ...), not a single repeated character. |
| `GB_ADMIN_TOKEN` / `GB_ADMIN_TOKEN_FILE` | none | **required while the admin listener is enabled.** At least 16 characters, same placeholder rules, must differ from the JWT secret. |
| `GB_CONFIG` | none | JSON config file (balance constants, intervals, directories). The keys `jwt_secret`, `admin_token`, `update_password`, `segment_password` and `external_trigger` are **rejected** in the file. Unknown keys are rejected. |
| `GB_UPDATE_MINUTES` | 30 | minutes between full updates |
| `GB_MOVES_PER_UPDATE` | 3 | movement segments per update |
| `GB_SHUTDOWN_TIMEOUT` | 30 | seconds allowed for an orderly shutdown |
| `GB_TURN_STALL_TIMEOUT` | 600 | seconds without turn progress before `/livez` reports the actor stuck |
| `GB_WS_ALLOWED_ORIGINS` | empty | comma-separated browser origins allowed to open WebSockets (`https://host[:port]`). Empty means same-origin only; requests without an `Origin` header (non-browser clients) are always allowed. |
| `GB_WS_MAX_MESSAGE_BYTES` | 4096 | per-frame read limit; larger frames close the connection |
| `GB_ENROLL_PREVIEW` | `false` | enables the **incomplete** web enrollment (see section 6) |
| `GB_LOG_LEVEL` | `info` | `debug`, `info`, `warn`, `error` |
| `GB_DOCS_DIR`, `GB_TLS_CERT`, `GB_TLS_KEY` | | help-file directory; TLS for the player listener |

Secrets: the server never logs them, never includes them in `/status`,
`/livez`, `/readyz`, `/metrics`, admin replies or error messages, and refuses
to read them from the config file. `*_FILE` variants read the first line of a
file (for mounted Kubernetes Secrets in Phase 2). Generate them with, for
example, `openssl rand -base64 48`.

### Example

```sh
export GB_JWT_SECRET="$(openssl rand -base64 48)"
export GB_ADMIN_TOKEN="$(openssl rand -base64 24)"
export GB_DB_PATH=/tmp/gb-dev/gb.db
mkdir -p /tmp/gb-dev
./gb-server
```

Logs are JSON lines on stdout (`log/slog`), one object per line with
`service`, `component`, `op`, `mode`, `duration_ms`, `err`, `player`,
`governor`, `turn`, `segment` and `session_id` where applicable. Legacy
`log.Printf` calls in gameplay files are bridged to the same format.

## 3. Universe initialisation (CR-05)

Ordinary startup **never generates or regenerates** a universe. A fresh
database starts the server in mode `UNINITIALIZED`: `/readyz` is 200 with
`game_ready: false`, player commands are refused, and the admin listener
accepts `universe init`:

```sh
export GBCTL_TOKEN="$GB_ADMIN_TOKEN"        # or GBCTL_TOKEN_FILE=/path/to/file
./gbctl universe init -seed 4242 -stars 100 -min-planets 2 -max-planets 10
```

The seed is required and recorded with the parameters, generator version and
time in the `meta/universe` record (`gbctl status` shows it); the same seed
and parameters regenerate the identical universe. The write is **one bbolt
transaction** (universe records and metadata together), so a failed or
interrupted initialisation leaves the store empty, never partially filled.
After a successful initialisation the running server loads the new world and
starts the schedule; no restart and no second database writer is involved.

Initialisation is refused (HTTP 409, `gbctl` exit 4) when the store already
holds a universe, including databases written by the pre-Phase-1 `makeuniv`.
Concurrent requests are serialized by the actor; the second one is refused.
There is **no overwrite option**. To recreate a *disposable development*
universe: stop the server, delete the database file, start the server,
initialise again. Do not do this to a database that holds player data.
(In Kubernetes the equivalent is a new empty volume, never an in-place
reset: `dev-volume-recovery.md`, ADR-0008.)

The offline `makeuniv -db PATH -seed N ...` tool does the same against a
database no server has open (it refuses a running server's file after the
bbolt lock timeout) and refuses an initialised database with exit 3;
`-skip-if-initialized` turns that into an exit-0 no-op for bootstrap scripts.

Races are not created by the server in Phase 1 (section 6); use `racegen`
offline.

## 4. Administration: `gbctl`

```sh
export GBCTL_SERVER=http://127.0.0.1:8081   # default
export GBCTL_TOKEN=...                      # or GBCTL_TOKEN_FILE=...
gbctl status                                # mode, schedule, counters, world size, universe record
gbctl update                                # run a full update now and wait for it
gbctl segment                               # run a movement segment now
gbctl quiesce                               # pause turns and mutating player commands; durable across restarts (section 7a)
gbctl resume                                # resume a quiesced engine (clears the maintenance record, reschedules overdue turns)
gbctl universe init -seed N [...]           # section 3
gbctl -json status                          # raw JSON reply
```

The token is never a command-line argument and is never printed. Exit codes:
0 ok, 1 usage or missing token, 2 cannot reach the server, 3 token rejected,
4 refused by the engine's state (halted, not serving, already initialised,
bad parameters), 5 server error, timeout, or a reply that does not confirm
success. `update` and `segment` return when the turn has finished; the
server also bounds them (default 10 minutes).

`gbctl` prints success only for a validated reply (GB1-06): the body must be
one complete JSON envelope with `"ok": true`, the requested operation name
and the engine status. An HTTP 2xx reply that is empty, truncated, not JSON,
of the wrong shape, for another operation or with `"ok": false` is reported
as an invalid reply (exit 5, "the outcome is unknown; check with 'gbctl
status' before retrying"). For `universe init` the reply's `result` must be
the universe record the server writes (GB1-06R): a JSON object with exactly
`seed`, `stars`, `min_planets`, `max_planets`, `planets`, `generator`,
`initialized_at` and `tool`, all present and in range, the seed equal to the
requested seed, any explicitly requested parameter echoed unchanged, and the
planet count consistent with the star count and planet bounds. A string, a
boolean, an array, an empty or partial object, an unknown member or a
mismatching value is reported the same way (exit 5), and `gbctl` prints the
validated record ("seed N, S stars, P planets ...") rather than raw JSON.
Operations that define no result (`update`, `segment`, `quiesce`, `resume`)
are rejected if a result is present.

**What a timeout means** (GB1-01). Every administrative operation, login,
welcome, enrollment and player command is a request to the single engine
actor. When the caller's deadline ends, the error says exactly which of
these happened:

| Error text | Meaning |
|---|---|
| `... not accepted ...: request was not executed and will not run` | refused at admission (deadline already over, or the queue stayed full); nothing ran |
| `... canceled while queued: request was not executed and will not run` | it was waiting behind other work; it is withdrawn and the engine skips it, now and later |
| `... request had already started; it continues to completion and is not rolled back` | the engine had begun it; it finishes (a turn is never interrupted), and its result is visible in `gbctl status`, the metrics and the log |
| `engine stopped` | the engine shut down before running it |

The tie-break between "canceled while queued" and "already started" is a
single atomic transition, so the two answers are never both true and a
withdrawn request never runs later. Canceled requests are counted in
`gb_requests_canceled_total{op}`, and admin results carry
`result="not_executed"` or `result="still_running"` in
`gb_admin_operations_total`.

The admin listener is a plain HTTP API under `/admin/v1/` with a static
bearer token compared in constant time (hashes of both sides). It is
separate from the player listener and bound to loopback by default. For the
Phase 2 Kubernetes deployment it is reached with `kubectl port-forward` to
the pod (it must not be put on the Service or the Ingress); whether it binds
loopback or `0.0.0.0` inside the pod is a Phase 2 manifest decision. The
static token is transitional and is replaced by the Phase 4b identity model.

The old `/admin/update` and `/admin/segment` password endpoints answer
HTTP 410 and never reach the engine.

## 5. Health, readiness, status and metrics

| Endpoint | Listener | Meaning |
|---|---|---|
| `GET /livez` | both | 200 while the actor goroutine heartbeats (every 10 s, stale after 60 s) **or** a running turn advanced its progress counter within `GB_TURN_STALL_TIMEOUT`. A long valid turn is never reported dead. `HALTED` and `QUIESCED` are alive. 503 only for a stuck or stopped actor. |
| `GET /readyz` | both | **Process** readiness for the orchestrator (CR-43, ADR-0010): 200 in `SERVING`, `TURN_IN_PROGRESS`, `UNINITIALIZED` (admin only, `game_ready: false`) and `QUIESCED` (operator maintenance, `game_ready: false`, `maintenance: true`, `maintenance_durable` when the store holds the record); 503 in `STARTING`, `HALTED` (including a `CORRUPT_STORE` start), `STOPPED` and while shutting down. **Gameplay** readiness is the body's `game_ready` (and `game_ready` in `gbctl status`), never the HTTP code. The body carries `mode`, `game_ready`, `draining`, `halt_reason`, `turn_running`, `maintenance`, `maintenance_durable`. |
| `GET /status` | player | mode, connected session count, schedule, counters, star and race counts. No player list, no secrets. |
| `GET /metrics` | admin | Prometheus text format. |

Both probes are served by the HTTP goroutines from atomics; they never wait
for the actor, so they answer during long turns and while halted.

Engine modes (`gbctl status`, `/readyz`): `STARTING`, `UNINITIALIZED`,
`SERVING`, `TURN_IN_PROGRESS`, `QUIESCED`, `HALTED`, `STOPPED`. `gbctl
status` also reports `game_ready` and, while quiesced, a `maintenance`
object (`durable`, `set_at`, `reason`, `error`); see section 7a.

Metrics (bounded labels; never per player or per session):
`gb_engine_mode{mode}`, `gb_commands_total{type,result}` (type is the
canonical command name or `unknown`), `gb_command_panics_total`,
`gb_turn_panics_total`, `gb_turn_duration_seconds{kind}`,
`gb_turns_total{kind,result}`, `gb_turn_progress`, `gb_actor_queue_depth`,
`gb_admin_operations_total{op,result}`, `gb_schedule_*`, `gb_world_*`,
`gb_maintenance_durable` (1 while the store holds the maintenance record),
`gb_enrollments_total{result}`, `gb_sessions_connected`,
`gb_logins_total{result}`, `gb_ws_dropped_sessions_total{reason}`,
`gb_ws_rejected_total{reason}`, `gb_http_requests_total{route,code}`.

## 6. Enrollment (CR-03, CR-07)

Web enrollment is **disabled by default**: `POST /api/enroll` answers 403
and `GET /enroll` shows a notice, and nothing is read or written. The reason
is CR-07: enrolled races get no home planet, sectors or ships and cannot
play; full placement is Phase 3c.

`GB_ENROLL_PREVIEW=true` enables the form for development. The request then
runs on the game actor against the server's own database handle (the old
handler opened the database a second time and timed out on the bbolt lock
after five seconds), validates as before, stores the race, adds it to the
running server's race list so it can log in at once, and every success
message states `PREVIEW ONLY ... not playable`. Do not enable it on a server
the public can reach.

## 7. Turn schedule and restarts (CR-21)

The schedule (next update, next segment, update and segment counters,
intervals) is persisted in `meta/schedule` after initialisation, after every
turn, after resume and during orderly shutdown, and restored at startup. An
orderly restart therefore continues the same schedule and the "DOING UPDATE
n" numbering does not restart. A restart of a **quiesced** server adopts the
schedule without applying the overdue policy (section 7a); the policy is
applied at `gbctl resume`.

Overdue policy (Phase 1, conservative; the final policy is a Phase 3
decision): turns that fell due while the server was down or quiesced are
**not** run automatically. An overdue update is rescheduled to now plus one
update interval (segments restart with it), the counters are kept, the
number of missed updates is logged and counted
(`gb_schedule_missed_turns_total`). Run `gbctl update` if the missed turn
should happen.

## 7a. Maintenance mode: a durable quiesce (CR-43, ADR-0010)

`gbctl quiesce` does two things, in this order: it writes the maintenance
record `meta/maintenance` (`{schema_version: 1, quiesced: true, set_at,
reason, engine_version}`) in its own bbolt transaction, then switches the
engine to `QUIESCED`. From then on scheduled turns do not fire (the actor
arms no timer outside `SERVING`), mutating player commands are refused
with the "paused" message, `update`/`segment` are refused (exit 4), and
**a restart starts `QUIESCED` again**: startup reads the record after the
CR-18 validation, adopts the schedule exactly as persisted (overdue times
stay in the past, nothing is written, no missed turn is counted) and
answers `/readyz` 200 with `game_ready: false`. This is what lets a
Kubernetes rolling update replace a quiesced Pod (`deploy.md` section 5).

`gbctl resume` deletes the record and writes the resumed schedule (the
no-catch-up policy of section 7 applied at that moment) in **one**
transaction, then switches to `SERVING`. The store is therefore always in
one of two states: quiesced with the old schedule, or serving with the new
one.

What `gbctl status` tells you:

| `mode` | `maintenance` | Meaning |
|---|---|---|
| `QUIESCED` | `durable: true`, `set_at`, `reason` | the record is in the store; a restart starts `QUIESCED` |
| `QUIESCED` | `durable: false`, `error` | the quiesce is **in memory only**: the record write failed (`error` says why); turns are still stopped, but a restart would start `SERVING`. Re-run `gbctl quiesce` (it retries the write) or treat the store as failing. `gbctl quiesce` itself returned exit 5 with `engine is QUIESCED in memory only …` |
| `SERVING` | absent | normal |
| `HALTED` with `halt_reason` `CORRUPT_STORE: corrupt maintenance record …` | absent | the record exists but is malformed (unknown field, `quiesced: false`, JSON null, wrong schema, trailing data). Nothing was written or repaired; the operator inspects `meta/maintenance` and decides. The same fail-closed start as a malformed commit record (section 9) |
| `HALTED` with `halt_reason` `resume: …` | absent (record still in the store) | the resume transaction failed; the next start is `QUIESCED` |

Ambiguous outcomes: if `gbctl quiesce` timed out or the process died
before replying, the record may or may not be in the store; `gbctl status`
after the next start says which (`QUIESCED`/`durable: true` or `SERVING`),
and re-issuing the quiesce is always safe (a durable double quiesce is a
no-op). If `gbctl resume` timed out, the store is either still quiesced or
serving with the new schedule, never half way; `gbctl status` says which,
and re-issuing `resume` on a `SERVING` engine is a no-op.

Stores written before this change carry no record and start as before
(`SERVING`, overdue policy). The record is written only by `quiesce`,
outside turns and through the write fence; commits never touch it, so the
shutdown flush performed while quiesced keeps it, and it is not part of
the world digest.

## 8. Shutdown

On `SIGINT`, `SIGTERM` or `SIGHUP` the server:

1. stops accepting new WebSocket upgrades and new player commands (refused
   with "paused or shutting down");
2. marks itself draining (`/readyz` 503) and stops the listeners;
3. closes every WebSocket session with close code 1012 (service restart);
4. lets a running turn finish (a turn is never interrupted; its result is
   one atomic commit, so stopping mid-body would only discard work);
5. commits in-memory state and the schedule in one bbolt transaction
   exactly as a turn boundary does (ships reloaded from the store first,
   then the whole world written from memory; commit record kind
   `shutdown`);
6. closes the database;
7. exits.

The guarantee of this orderly path (one signal): if the running turn and
the shutdown commit finish within `GB_SHUTDOWN_TIMEOUT`, the store holds the
complete state as of the shutdown commit and the process exits 0. In every
other outcome the store holds the **last complete commit** (a turn's or the
shutdown's); what is lost is only the in-memory work since that commit.

Exit status (each case is logged with `op=shutdown`):

| Code | Meaning |
|---|---|
| 0 | orderly shutdown after a signal; state flushed and the database closed |
| 1 | configuration or startup error, including a listener that cannot bind its address; nothing was served |
| **2** | the engine was `HALTED`: in-memory state was discarded, not flushed. The store holds a complete commit, but which one depends on why the engine halted, and the halt reason (`gbctl status`, the `op=halt` log line) says so: after a **confirmed aborted** turn (panic in the body, fence violation, or a transaction whose record read back unchanged) there is no new turn commit and the store is the previous one, with memory reloaded from it; after a **confirmed committed** turn whose notification delivery then failed, the new commit is durable and stands; after an **uncertain** outcome (record unreadable, missing or unexpectedly changed) persistence cannot be asserted either way and the operator compares `commit_seq` with the expected turn before anything else; if the reload after a failure itself failed, the reason says memory was `untrusted`. In every case the operator reviews the reason before restarting. In-memory changes made by player commands since the last commit, other than what those commands wrote per record themselves, are lost |
| **3** | the shutdown budget expired while a turn was still running: the process exits anyway and the turn may still commit before the process is gone. The commit is atomic, so the store holds either the previous commit or the completed one, never a mixture; `commit_seq` at the next start (or the commit record's turn counter) says which. Nothing here claims the turn was lost or kept |
| **4** | the shutdown commit failed (the store holds the previous commit), its outcome is uncertain (check `commit_seq` at the next start), or the database close reported an error |
| **5** | a listener failed while serving (for example a missing or unreadable TLS certificate or key). The shutdown sequence still runs and its own result is logged as `shutdown_exit_code`, but the process exits 5: cleanup never turns a failure into success (GB1-05) |
| **6** | forced exit: a **second** `SIGINT`/`SIGTERM` arrived during shutdown. The process logs the forced exit and exits at once, without waiting for the engine or closing the database. The store keeps its last complete commit; anything uncommitted in memory is lost. Use it only when the orderly path is stuck (GB1-09) |

A second signal is deliberately a forced exit rather than being ignored:
an operator who sends it wants the process gone, and the explicit exit code
and log line make that outcome observable instead of a bare death by signal.
`kill -9` remains possible and has the same consequences as code 6 without
the log line.

## 9. Panics, failed commits and the HALTED mode

A panic in a command handler or in turn processing is recovered on the
actor goroutine, logged with its stack (`op=halt`), counted, and the engine
enters `HALTED`: player commands other than `who`, `help` and `quit` are
refused with a message, scheduled and administrative turns are refused,
`gbctl resume` is refused, `/readyz` is 503 and `/livez` stays 200 so an
orchestrator does not restart-loop the process and the state stays
inspectable (`gbctl status` shows `halt_reason`). Recovery is a **process
restart**, which reloads the store. The same fail-closed path is taken when
`gbctl resume` cannot persist the rescheduled times (GB1-03).

**A turn is never half applied; what a failed turn left behind is
established, not assumed (CR-18, ADR-0009).** While a turn body runs, the
store refuses every write except the turn's own commit (the write fence;
number allocation is staged in memory and persisted by the commit), and
the news, telegrams and messages the turn produces are held back. When a
turn does not complete normally the engine halts and reports one of the
following outcomes, each decided by reading the durable commit record back
and comparing its **complete identity** (schema version, sequence, turn,
segment, kind, wall time, engine version) with the record that existed
before the turn and with the record the commit would have written:

- **Confirmed aborted transaction.** The body panicked, turn code hit the
  write fence, or the commit transaction failed or panicked and the record
  read back is identical to the record before the turn (or still absent on
  a store that had none). No new turn commit exists. The held effects and
  staged allocations are dropped, the in-memory world and schedule are
  reloaded from that last commit, players are told the turn was rolled
  back, and the halt reason ends in `nothing of the turn was written;
  in-memory state reloaded from the last commit` (a fence violation names
  the refused store method).
- **Confirmed committed transaction, delivery failed.** The transaction
  committed and only the delivery of the held notifications panicked. The
  committed state is durable and stands; the halt reason says `committed
  (commit_seq N) ... nothing was rolled back`; store and memory agree.
- **Uncertain outcome.** The commit record could not be read back or is
  malformed, has disappeared, is identical to the record this commit would
  have written although a failure was reported (the transaction became
  visible; durability on disk is not asserted), or differs from both known
  identities (including an altered record carrying the previous sequence).
  Persistence cannot be asserted either way. The halt reason says `outcome
  uncertain` and never claims a rollback; no held notification is
  delivered; memory is reloaded from the store as it is now visible;
  `commit_seq` in `gbctl status` shows what the store holds and the
  operator compares it with the expected turn.
- **Reload failed.** In the aborted and uncertain cases, if reloading the
  world, the schedule or the commit record fails, the reason says
  `in-memory state is untrusted` instead of `reloaded`.

The engine stays `HALTED` in all of these; the operator reads the halt
reason and `commit_seq` before restarting, because the right follow-up
differs (an aborted or uncertain turn is overdue after the restart and is
run by hand with `gbctl update` once the store's state is confirmed; a
committed turn must not be run again).

In every case the halted shutdown writes nothing. After a restart a turn
that was not committed is simply overdue: the no-catch-up policy
reschedules it and `gbctl update` runs it by hand.
`gb_turns_total{result="panic"|"fence_violation"|"commit_failed"|"commit_uncertain"}`,
`gb_commits_total{result="ok"|"error"|"panic"|"uncertain"}`,
`gb_store_fenced_writes_total` and `gb_turn_notice_delivery_panics_total`
show what happened. Scheduled turns, `gbctl update`/`segment` and the
shutdown commit all follow this path.

What a restart gives you after a **command** panic: the store as of the
last commit plus whatever the panicking command and earlier commands had
already written per record (commands still write per record, CR-17; the
staging set of plan 8.4 is later Phase 3a work). In-memory changes since
the last commit that no command had written are lost.

`gbctl status` reports `commit_seq`, the sequence number of the last
commit; a store written before this change reports 0 until its first
commit. At startup the engine validates the commit metadata and starts
`HALTED` with reason `CORRUPT_STORE…` (alive, not ready, writing nothing,
no fresh schedule created) when an existing commit record is JSON null,
empty, undecodable, of an unsupported schema version or unknown kind, has
a zero or overflowing sequence or negative counters, when the schedule
record is undecodable or negative, when a non-init commit record has no
schedule record, or when the commit record's turn is **ahead** of the
schedule record (something wrote the store outside the commit path). An
absent commit record (older store) is accepted; so is an init record
without a schedule (fresh universe) and a schedule ahead of the commit
record (turns run by an older binary; warned, resynchronised at the next
commit).

## 10. Persistence guarantees and remaining limitations

**Guaranteed (CR-18 implemented, independent acceptance pending; ADR-0009):**
every update, movement segment and orderly-shutdown flush commits the whole
world (ships, stars, planets, races, star data, power, blocks), the
schedule counters and times, the turn's news and stored telegrams and the
free-list numbers the turn allocated in **one bbolt transaction** together
with a validated commit record (`meta/commit`: sequence, turn, segment,
kind, wall time, engine version). A crash, `kill -9`, power loss or budget
timeout at any point leaves the store at a complete commit (the previous
or, if the transaction had committed, the new one); a turn is never
counted without its world and never half applied. The tests in
`go/internal/game/commit_test.go`, `commit_remediation_test.go`,
`go/internal/db/commit_test.go` and `go/test/e2e/crash_test.go` (a real
process killed during the turn body, and one exiting from inside the open
commit transaction) assert this; the known-defect entry
`persistence/non-atomic-turn-flush` records the pending acceptance.

**Not yet guaranteed:** player commands between turns still write per
record (`PutShip`, `PutPlanet`, `PutRace` at about 70 sites) and six
handlers read planets through the store (CR-11, CR-17), so a command that
fails or is interrupted between two of its own writes is not atomic, a
command that changes memory without writing is persisted only by the next
commit, and not every database mutation is turn-atomic. Live notifications
after a commit are best-effort (a disconnect between generation and
delivery loses them; a delivery failure halts the engine but never undoes
the commit).
Plan 8.4 (command staging set, group commit, acknowledged-after-commit),
per-record commit stamps and the `TURN_FAILED` / `RetryTurn` operator path
remain Phase 3a work. Keep the shutdown budget above the longest observed
turn (`gb_turn_duration_seconds`) so that an orderly stop commits rather
than discards the running turn, and prefer `gbctl quiesce` before a planned
restart during busy periods.

Other known operational limitations: `Authenticate`, enrollment and the
welcome message run on the actor, so a login during a long turn waits up to
10 s and then answers 503 (CR-20 interim); a player command queued behind a
long turn waits up to two minutes to start, after which it is withdrawn and
the player is told it was **not** run (it never runs later); there is no
login rate limit; tokens are still accepted in the WebSocket URL query
string (review section 3.5); the C universe-generation algorithm is
unchanged, including its known quirks.

### WebSocket sessions during long commands (GB1-04)

Each session has three parts: a read loop that only reads frames, a single
writer goroutine that owns every write to the socket, and a single command
worker. The read loop never waits for the engine, so pong replies keep
refreshing the 60 s read deadline while a command or a turn runs for
minutes; a client that really stops responding is still dropped by that
deadline. The worker runs the session's commands one at a time in arrival
order from a queue of 32; further commands are refused with "Too many
commands waiting" (`gb_ws_commands_rejected_total{reason="queue_full"}`)
rather than buffered without limit. When a session closes, its queued
commands are withdrawn (never run) and a command that already started
finishes; shutdown still closes every session with 1012 at once.

## 11. Test-only knobs

`GB_TEST_HOOKS=1` together with `GB_TEST_TURN_DELAY=<duration>` makes every
turn sleep for that long (advancing the progress counter) so that the
end-to-end tests can observe a turn in flight. Never set these in a real
configuration; the server logs `TEST HOOK ACTIVE` when they are.
