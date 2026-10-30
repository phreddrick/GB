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
gbctl quiesce                               # pause turns and mutating player commands
gbctl resume                                # resume a quiesced engine
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
| `GET /readyz` | both | 200 in `SERVING`, `TURN_IN_PROGRESS` and `UNINITIALIZED` (admin only, `game_ready: false`); 503 in `STARTING`, `QUIESCED`, `HALTED`, `STOPPED` and while shutting down. The body carries `mode`, `game_ready`, `draining`, `halt_reason`, `turn_running`. |
| `GET /status` | player | mode, connected session count, schedule, counters, star and race counts. No player list, no secrets. |
| `GET /metrics` | admin | Prometheus text format. |

Both probes are served by the HTTP goroutines from atomics; they never wait
for the actor, so they answer during long turns and while halted.

Engine modes (`gbctl status`, `/readyz`): `STARTING`, `UNINITIALIZED`,
`SERVING`, `TURN_IN_PROGRESS`, `QUIESCED`, `HALTED`, `STOPPED`.

Metrics (bounded labels; never per player or per session):
`gb_engine_mode{mode}`, `gb_commands_total{type,result}` (type is the
canonical command name or `unknown`), `gb_command_panics_total`,
`gb_turn_panics_total`, `gb_turn_duration_seconds{kind}`,
`gb_turns_total{kind,result}`, `gb_turn_progress`, `gb_actor_queue_depth`,
`gb_admin_operations_total{op,result}`, `gb_schedule_*`, `gb_world_*`,
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
n" numbering does not restart.

Overdue policy (Phase 1, conservative; the final policy is a Phase 3
decision): turns that fell due while the server was down or quiesced are
**not** run automatically. An overdue update is rescheduled to now plus one
update interval (segments restart with it), the counters are kept, the
number of missed updates is logged and counted
(`gb_schedule_missed_turns_total`). Run `gbctl update` if the missed turn
should happen.

## 8. Shutdown

On `SIGINT`, `SIGTERM` or `SIGHUP` the server:

1. stops accepting new WebSocket upgrades and new player commands (refused
   with "paused or shutting down");
2. marks itself draining (`/readyz` 503) and stops the listeners;
3. closes every WebSocket session with close code 1012 (service restart);
4. lets a running turn finish (a turn is never interrupted: turn code writes
   ships, telegrams and news to the store while it runs, so there is no
   point before the flush at which stopping would leave nothing written);
5. flushes in-memory state exactly as a turn boundary would (ships reloaded
   from the store first, then stars, planets and races written from memory)
   and persists the schedule;
6. closes the database;
7. exits.

The guarantee of this orderly path (one signal): if the running turn and
the flush finish within `GB_SHUTDOWN_TIMEOUT`, the store holds the complete
state as of the shutdown flush and the process exits 0. Nothing stronger is
claimed for the other outcomes.

Exit status (each case is logged with `op=shutdown`):

| Code | Meaning |
|---|---|
| 0 | orderly shutdown after a signal; state flushed and the database closed |
| 1 | configuration or startup error, including a listener that cannot bind its address; nothing was served |
| **2** | the engine was `HALTED`: in-memory state was discarded, not flushed. The store is **not** guaranteed to hold a complete turn: if the halt came from a panic or a failed write during a turn, the store may already contain partially applied turn changes (CR-18, section 10) |
| **3** | the shutdown budget expired while a turn was still running: the process exits anyway and the store may contain a partially applied turn |
| **4** | the flush or the database close reported an error |
| **5** | a listener failed while serving (for example a missing or unreadable TLS certificate or key). The shutdown sequence still runs and its own result is logged as `shutdown_exit_code`, but the process exits 5: cleanup never turns a failure into success (GB1-05) |
| **6** | forced exit: a **second** `SIGINT`/`SIGTERM` arrived during shutdown. The process logs the forced exit and exits at once, without waiting for the engine or closing the database. No clean-shutdown guarantee applies: a turn or flush in progress may leave partially applied changes (CR-18). Use it only when the orderly path is stuck (GB1-09) |

A second signal is deliberately a forced exit rather than being ignored:
an operator who sends it wants the process gone, and the explicit exit code
and log line make that outcome observable instead of a bare death by signal.
`kill -9` remains possible and has the same consequences as code 6 without
the log line.

## 9. Panics and the HALTED mode

A panic in a command handler or in turn processing is recovered on the
actor goroutine, logged with its stack (`op=halt`), counted, and the engine
enters `HALTED`: player commands other than `who`, `help` and `quit` are
refused with a message, scheduled and administrative turns are refused,
`gbctl resume` is refused, `/readyz` is 503 and `/livez` stays 200 so an
orchestrator does not restart-loop the process and the state stays
inspectable (`gbctl status` shows `halt_reason`). Nothing is flushed: the
in-memory state may be inconsistent. Recovery is a **process restart**,
which reloads the store. The same fail-closed path is taken when a turn's
flush or the schedule write returns an error, and when `gbctl resume` cannot
persist the rescheduled times (GB1-03): the resume reports the failure, the
engine enters `HALTED` instead of `SERVING`, `game_ready` stays false, and
the status keeps showing the last schedule that was actually stored.

What a restart gives you: whatever the store holds, which is **not**
necessarily a complete turn. Turn code writes ships, telegrams and news to
the store while it runs, and the flush is many separate transactions, so a
panic or failed write in the middle of a turn can leave the store with
partially applied turn changes (CR-18, section 10). The halted shutdown
does not make this worse (it writes nothing), but it cannot undo it either.
In-memory changes since the last flush are lost. Phase 3a replaces this
interim policy with reload-on-failure and a single-transaction commit.

## 10. Persistence limitation that remains (CR-18)

A turn is flushed in many bbolt transactions (one per ship batch, star,
planet and race, plus power, blocks and star data). An orderly shutdown
preserves the universe and the schedule; a crash, a `kill -9`, a power loss
or a shutdown-budget timeout **during a flush** can leave some records at
turn N+1 and others at turn N, with no stamp to detect it. Phase 1 does not
fix this and no Phase 1 test asserts crash consistency; the entry
`persistence/non-atomic-turn-flush` in `test/knowndefects.yaml` keeps it
visible until Phase 3a's single-transaction commit and crash-injection
tests. Keep the shutdown budget comfortably above the longest observed turn
(`gb_turn_duration_seconds`) and prefer `gbctl quiesce` before a planned
restart during busy periods.

Other known operational limitations in Phase 1: the planet records that six
handlers read and write through the store directly (CR-17) can be reverted
by any flush, including the shutdown flush; `Authenticate`, enrollment and
the welcome message run on the actor, so a login during a long turn waits
up to 10 s and then answers 503 (CR-20 interim); a player command queued
behind a long turn waits up to two minutes to start, after which it is
withdrawn and the player is told it was **not** run (it never runs later);
there is no login rate limit; tokens are still accepted in the WebSocket
URL query string (review section 3.5); the C universe-generation algorithm
is unchanged, including its known quirks.

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
