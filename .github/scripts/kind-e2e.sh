#!/usr/bin/env bash
# Disposable kind end-to-end test of the Phase 2 deployment artifacts on a
# GitHub-hosted runner (Phase 2C brief section 7; migration plan 13.3).
#
#   kind-e2e.sh <gb-server.oci.tar> <gbctl.docker.tar>
#
# Environment (set by the workflow): KIND_NODE_IMAGE (pinned kindest/node by
# digest), KUBECONFIG (a private file this script writes; it must not point
# at an existing kubeconfig), WORK_DIR, DIAG_DIR (diagnostics written on
# failure; never Secret objects), KIND_OWNERSHIP_FILE (runner-local marker,
# default $WORK_DIR/ownership.json; must not exist yet), the run identity
# GITHUB_REPOSITORY, GITHUB_RUN_ID, GITHUB_RUN_ATTEMPT, GITHUB_JOB, and
# optionally KIND_CLUSTER (default: a name unique to this run and attempt,
# gb-ci-r<run id>a<attempt>-<6 random hex>, validated against
# ^gb-ci[a-z0-9-]*$ and 63 characters).
#
# Refuses to run outside GitHub Actions unless GB_KIND_E2E_ALLOW_LOCAL=1: the
# development host runs no container runtime by policy, and this script
# creates and deletes a Kubernetes cluster. Every kubectl call goes through
# the kubeconfig kind writes for the new cluster, whose only context is
# kind-<name>; the script asserts that before touching anything, so it can
# never address another cluster.
#
# Ownership (GB2C-04R): a cluster of the chosen name that already exists is
# refused before creation and is never adopted or deleted. Only after `kind
# create cluster` has exited 0 does this script write the ownership marker
# (cluster name, repository, run id, attempt, job) to KIND_OWNERSHIP_FILE;
# a refused or failed creation writes nothing, so neither the exit trap nor
# the workflow's fallback step may delete anything (kind itself removes what
# a failed creation left behind unless --retain is given, which it is not).
# The only deletion this script performs is of its own cluster, in every
# exit path after a recorded creation, through kind-cleanup.sh, which
# validates the marker against this run's identity before `kind delete
# cluster` and fails the job unless an independent listing confirms absence.
#
# What it proves: the image starts under Pod Security `restricted` as UID
# 65532 with a read-only root and a writable mounted volume; probes behave;
# `gbctl universe init` succeeds once and is refused (exit 4) afterwards; an
# orderly restart keeps universe and schedule; the cluster is deleted. What
# it does not prove: Longhorn RWOP, NetworkPolicy enforcement (kind's default
# CNI does not enforce the policies that are applied here), Traefik routing,
# PodMonitor discovery, node fencing, or a player login (no race exists on a
# fresh universe and the offline fixture writer is not available in-cluster).
set -euo pipefail

SERVER_OCI=${1:?gb-server OCI archive}
GBCTL_DOCKER=${2:?gbctl docker archive}
NODE_IMAGE=${KIND_NODE_IMAGE:?pinned kind node image}
WORK=${WORK_DIR:-$(mktemp -d)}
DIAG=${DIAG_DIR:-$WORK/diagnostics}
OWNERSHIP=${KIND_OWNERSHIP_FILE:-$WORK/ownership.json}
for v in GITHUB_REPOSITORY GITHUB_RUN_ID GITHUB_RUN_ATTEMPT GITHUB_JOB; do
  [ -n "${!v:-}" ] || { echo "kind-e2e: $v is not set; the cluster name and its ownership marker are derived from the run identity" >&2; exit 2; }
done
if [ -n "${KIND_CLUSTER:-}" ]; then
  CLUSTER=$KIND_CLUSTER
else
  [[ "$GITHUB_RUN_ID" =~ ^[0-9]{1,20}$ ]] && [[ "$GITHUB_RUN_ATTEMPT" =~ ^[0-9]{1,5}$ ]] || { echo "kind-e2e: GITHUB_RUN_ID/GITHUB_RUN_ATTEMPT are not numeric" >&2; exit 2; }
  CLUSTER="gb-ci-r${GITHUB_RUN_ID}a${GITHUB_RUN_ATTEMPT}-$(od -An -N3 -tx1 /dev/urandom | tr -d ' \n')"
fi
[[ "$CLUSTER" =~ ^gb-ci[a-z0-9-]*$ ]] && [ "${#CLUSTER}" -le 63 ] || { echo "kind-e2e: cluster name must match ^gb-ci[a-z0-9-]*$ and be at most 63 characters (got '$CLUSTER'); only such a cluster is ever created or deleted" >&2; exit 2; }
NS=gb-dev
PLAYER_PORT=18080
ADMIN_PORT=18081
BUSYBOX=busybox@sha256:a7dbc319b1438115a207e11930dc4bb3e643dcb3ec0bdd694bb36cd8fad289ec # busybox:1.37.0 index digest, Docker Hub, 2026-10-30
HERE=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
REPO=$(cd "$HERE/../.." && pwd)

if [ "${GITHUB_ACTIONS:-}" != "true" ] && [ "${GB_KIND_E2E_ALLOW_LOCAL:-}" != "1" ]; then
  echo "kind-e2e: refusing to run outside GitHub Actions (set GB_KIND_E2E_ALLOW_LOCAL=1 on a host that may run kind; never the development host)" >&2
  exit 2
fi
for tool in kind kubectl docker curl jq openssl python3 timeout; do
  command -v "$tool" >/dev/null || { echo "kind-e2e: $tool is required" >&2; exit 2; }
done
mkdir -p "$WORK" "$DIAG"
chmod 700 "$WORK"
: "${KUBECONFIG:=$WORK/kubeconfig}"
export KUBECONFIG
[ ! -e "$KUBECONFIG" ] || { echo "kind-e2e: KUBECONFIG $KUBECONFIG already exists; refusing to reuse a kubeconfig" >&2; exit 2; }
[ ! -e "$OWNERSHIP" ] && [ ! -L "$OWNERSHIP" ] || { echo "kind-e2e: ownership marker $OWNERSHIP already exists; refusing to run with stale ownership evidence" >&2; exit 2; }
[ -d "$(dirname "$OWNERSHIP")" ] && [ -w "$(dirname "$OWNERSHIP")" ] || { echo "kind-e2e: ownership marker directory $(dirname "$OWNERSHIP") is not writable" >&2; exit 2; }
existing=$(timeout 60 kind get clusters 2>/dev/null) || { echo "kind-e2e: 'kind get clusters' failed; cannot prove that no cluster named $CLUSTER exists" >&2; exit 2; }
grep -qx "$CLUSTER" <<<"$existing" && { echo "kind-e2e: a cluster named $CLUSTER already exists; it is not ours and will be neither used nor deleted" >&2; exit 2; }
echo "kind-e2e: cluster name $CLUSTER (run $GITHUB_RUN_ID attempt $GITHUB_RUN_ATTEMPT job $GITHUB_JOB); ownership marker $OWNERSHIP"

STEP=0
FAILED=1
CREATED=0
PF_PID=""
LOG_PID=""
step() { STEP=$((STEP + 1)); echo; echo "== [$STEP] $*"; }
fail() { echo "FAIL $*" >&2; exit 1; }
ok() { echo "PASS $*"; }
k() { kubectl --context "kind-$CLUSTER" -n "$NS" "$@"; }

collect_diagnostics() {
  # Never `get secret`: nothing below prints Secret contents.
  echo "collecting diagnostics into $DIAG"
  {
    kubectl --context "kind-$CLUSTER" get nodes -o wide
    k get all,pvc,networkpolicy,serviceaccount,configmap -o wide
    k get events --sort-by=.lastTimestamp
    k describe statefulset/gb-engine
    k describe pod/gb-engine-0
  } > "$DIAG/kubectl.txt" 2>&1 || true
  k logs pod/gb-engine-0 --all-containers=true > "$DIAG/gb-engine-0.log" 2>&1 || true
  k logs pod/gb-engine-0 --all-containers=true --previous > "$DIAG/gb-engine-0.previous.log" 2>&1 || true
  cp -f "$WORK"/*.log "$DIAG"/ 2>/dev/null || true
  cp -f "$WORK"/*.json "$DIAG"/ 2>/dev/null || true
}

cleanup() {
  # The trap preserves the test's own status and hands the deletion, the
  # independent inspection and the verdict to kind-cleanup.sh (GB2C-04): a
  # failed deletion or a failed inspection fails the job even when every test
  # step passed, and a test failure is never overwritten by a clean cleanup.
  # Deletion is authorised only by the ownership marker written after a
  # successful creation (GB2C-04R); without it nothing is deleted.
  rc=$?
  trap - EXIT
  set +e
  [ -n "$PF_PID" ] && kill "$PF_PID" 2>/dev/null
  [ -n "$LOG_PID" ] && kill "$LOG_PID" 2>/dev/null
  main_status=$rc
  if [ "$FAILED" -ne 0 ] && [ "$main_status" -eq 0 ]; then main_status=1; fi
  if [ "$main_status" -ne 0 ] && [ "$CREATED" -eq 1 ]; then
    listing=$(timeout 60 kind get clusters 2>/dev/null) && grep -qx "$CLUSTER" <<<"$listing" && collect_diagnostics
  fi
  rm -f "$WORK/secrets/"* 2>/dev/null
  echo
  if [ "$CREATED" -eq 0 ]; then
    # No creation was recorded by this run (a precondition failed, or `kind
    # create cluster` was refused or failed): nothing of ours exists to
    # delete, no ownership marker was written, and the fallback step will
    # find none and delete nothing.
    echo "kind-e2e: FAILED before a cluster creation was recorded (exit $main_status); no ownership marker, nothing deleted" >&2
    exit "$main_status"
  fi
  "$HERE/kind-cleanup.sh" --ownership "$OWNERSHIP" --cluster "$CLUSTER" --kubeconfig "$KUBECONFIG" --main-status "$main_status" --timeout 300
  exit $?
}
trap cleanup EXIT

wait_for() { # wait_for <seconds> <description> <command...>
  local secs=$1 what=$2; shift 2
  local i
  for ((i = 0; i < secs; i++)); do
    if "$@" >/dev/null 2>&1; then return 0; fi
    sleep 1
  done
  echo "timed out after ${secs}s waiting for $what" >&2
  return 1
}

start_port_forward() {
  [ -n "$PF_PID" ] && { kill "$PF_PID" 2>/dev/null || true; PF_PID=""; }
  k port-forward pod/gb-engine-0 "$PLAYER_PORT:8080" "$ADMIN_PORT:8081" > "$WORK/port-forward.log" 2>&1 &
  PF_PID=$!
  wait_for 60 "port-forward to gb-engine-0" curl -fsS "http://127.0.0.1:$PLAYER_PORT/readyz" || { cat "$WORK/port-forward.log"; fail "port-forward did not come up"; }
}

gbctl() { # gbctl <args...>: the gbctl image, host network, token from a file owned by 65532
  docker run --rm --network host \
    -e GBCTL_SERVER="http://127.0.0.1:$ADMIN_PORT" -e GBCTL_TOKEN_FILE=/run/secrets/admin-token \
    -v "$WORK/secrets/admin-token:/run/secrets/admin-token:ro" gbctl:ci "$@"
}

# ----------------------------------------------------------------- cluster --
step "create kind cluster $CLUSTER ($NODE_IMAGE)"
create_rc=0
kind create cluster --name "$CLUSTER" --image "$NODE_IMAGE" --wait 180s --kubeconfig "$KUBECONFIG" || create_rc=$?
[ "$create_rc" -eq 0 ] || fail "kind create cluster exited $create_rc; no ownership recorded (kind removes an incomplete cluster itself; nothing is deleted by this run)"
# Ownership is recorded only now, after a successful creation, atomically.
ownership_tmp="$OWNERSHIP.tmp.$$"
( umask 077; jq -n --arg cluster "$CLUSTER" --arg repo "$GITHUB_REPOSITORY" --arg run "$GITHUB_RUN_ID" --arg attempt "$GITHUB_RUN_ATTEMPT" --arg job "$GITHUB_JOB" --arg at "$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
    '{kind: "gb-kind-e2e-ownership", state: "owned", cluster: $cluster, repository: $repo, run_id: $run, run_attempt: $attempt, job: $job, created_at: $at}' > "$ownership_tmp" ) \
  && mv -T "$ownership_tmp" "$OWNERSHIP" && [ -f "$OWNERSHIP" ] \
  || { rm -f "$ownership_tmp"; echo "FAIL could not write the ownership marker $OWNERSHIP; cluster $CLUSTER was created by this run but its deletion cannot be authorised automatically" >&2; exit 1; }
CREATED=1
ok "cluster created; ownership recorded in $OWNERSHIP"
contexts=$(kubectl config get-contexts -o name)
[ "$contexts" = "kind-$CLUSTER" ] || fail "kubeconfig has contexts other than kind-$CLUSTER: $contexts"
server_version=$(kubectl --context "kind-$CLUSTER" version -o json | jq -r .serverVersion.gitVersion)
grep -q '^v1\.36\.' <<<"$server_version" || fail "unexpected server version $server_version"
ok "cluster up, single context kind-$CLUSTER, server $server_version"

step "load images"
docker load -i "$GBCTL_DOCKER" >/dev/null
docker image inspect gbctl:ci --format '{{.Id}}' >/dev/null || fail "gbctl:ci not loaded"
node="${CLUSTER}-control-plane"
if kind load image-archive "$SERVER_OCI" --name "$CLUSTER" && docker exec "$node" crictl images -o json | jq -e '.images[].repoTags[]? | select(. == "docker.io/library/gb-server:ci")' >/dev/null; then
  ok "gb-server:ci loaded into the node from the OCI archive"
else
  echo "OCI archive import did not yield docker.io/library/gb-server:ci; loading through the docker engine instead"
  docker load -i "${SERVER_OCI%.oci.tar}.docker.tar" >/dev/null
  kind load docker-image gb-server:ci --name "$CLUSTER"
  docker exec "$node" crictl images -o json | jq -e '.images[].repoTags[]? | select(. == "docker.io/library/gb-server:ci")' >/dev/null || fail "gb-server:ci not present on the node"
  ok "gb-server:ci loaded into the node through the docker engine"
fi
expected_config=$(python3 "$HERE/oci_digest.py" "$SERVER_OCI" --field config_digest)
node_id=$(docker exec "$node" crictl images -o json | jq -r '.images[] | select(.repoTags[]? == "docker.io/library/gb-server:ci") | .id')
[ "$node_id" = "$expected_config" ] || fail "image on the node ($node_id) is not the built artifact ($expected_config)"
ok "node image id equals the archive config digest $expected_config"

# --------------------------------------------------------------- namespace --
step "namespace $NS with Pod Security restricted, test-only Secret"
kubectl --context "kind-$CLUSTER" create namespace "$NS"
kubectl --context "kind-$CLUSTER" label namespace "$NS" \
  pod-security.kubernetes.io/enforce=restricted pod-security.kubernetes.io/enforce-version=latest \
  pod-security.kubernetes.io/warn=restricted pod-security.kubernetes.io/audit=restricted
mkdir -p "$WORK/secrets"
chmod 700 "$WORK/secrets"
( umask 077; openssl rand -base64 48 > "$WORK/secrets/jwt-secret"; openssl rand -base64 24 > "$WORK/secrets/admin-token" )
if [ "${GITHUB_ACTIONS:-}" = "true" ]; then
  echo "::add-mask::$(cat "$WORK/secrets/jwt-secret")"
  echo "::add-mask::$(cat "$WORK/secrets/admin-token")"
fi
k create secret generic gb-server-secrets --from-file=jwt-secret="$WORK/secrets/jwt-secret" --from-file=admin-token="$WORK/secrets/admin-token" >/dev/null
# The gbctl container (UID 65532) must be able to read the token file.
chmod 0400 "$WORK/secrets/admin-token"
sudo chown 65532:65532 "$WORK/secrets/admin-token"
ok "namespace labelled restricted; Secret gb-server-secrets created from random values (not printed)"

# ------------------------------------------------------------------ apply --
step "apply apps/ci/platform"
k apply -k "$REPO/deploy/kustomize/apps/ci/platform"
[ "$(k get networkpolicy -o name | wc -l)" -eq 4 ] || fail "expected 4 NetworkPolicies"
k get serviceaccount gb-engine >/dev/null
ok "platform: ServiceAccount and 4 NetworkPolicies (not enforced by kind's CNI; structure only)"

step "apply apps/ci/server and wait for the StatefulSet"
k apply -k "$REPO/deploy/kustomize/apps/ci/server"
k rollout status statefulset/gb-engine --timeout=240s
k wait pod/gb-engine-0 --for=condition=Ready --timeout=120s
if k get events --field-selector reason=FailedCreate -o name | grep -q .; then k get events --field-selector reason=FailedCreate; fail "FailedCreate events (Pod Security?)"; fi
[ "$(k get pvc world-gb-engine-0 -o jsonpath='{.status.phase}')" = "Bound" ] || fail "PVC world-gb-engine-0 not Bound"
[ "$(k get pvc world-gb-engine-0 -o jsonpath='{.spec.accessModes[0]}')" = "ReadWriteOnce" ] || fail "ci PVC access mode is not ReadWriteOnce"
pod_uid_1=$(k get pod gb-engine-0 -o jsonpath='{.metadata.uid}')
ok "gb-engine-0 Ready under restricted PSS; PVC world-gb-engine-0 Bound (RWO, kind local-path; says nothing about Longhorn RWOP)"

# ----------------------------------------------------------- node checks --
step "node-side runtime facts (crictl on the kind node)"
cid=$(docker exec "$node" crictl ps -q --name gb-server --state running)
[ -n "$cid" ] || fail "no running gb-server container on the node"
docker exec "$node" crictl inspect "$cid" > "$WORK/crictl-inspect.json"
pid=$(jq -r '.info.pid' "$WORK/crictl-inspect.json")
[ "$(jq -r '.info.runtimeSpec.root.readonly' "$WORK/crictl-inspect.json")" = "true" ] || fail "runtime spec root is not read-only"
[ "$(jq -r '.info.runtimeSpec.process.user.uid' "$WORK/crictl-inspect.json")" = "65532" ] || fail "runtime spec uid is not 65532"
[ "$(jq -r '.info.runtimeSpec.process.noNewPrivileges' "$WORK/crictl-inspect.json")" = "true" ] || fail "noNewPrivileges is not set"
[ "$(jq -r '.info.runtimeSpec.process.capabilities.bounding | length' "$WORK/crictl-inspect.json")" = "0" ] || fail "bounding capabilities are not empty"
uid_line=$(docker exec "$node" cat "/proc/$pid/status" | grep '^Uid:')
[ "$(awk '{print $2, $3, $4, $5}' <<<"$uid_line")" = "65532 65532 65532 65532" ] || fail "process uids: $uid_line"
# mountinfo fields: id parent major:minor root mountpoint options ...
docker exec "$node" cat "/proc/$pid/mountinfo" > "$WORK/mountinfo.txt"
awk '$5 == "/" && $6 ~ /^ro(,|$)/ { found = 1 } END { exit found ? 0 : 1 }' "$WORK/mountinfo.txt" || fail "root mount is not ro in mountinfo"
awk '$5 == "/data/world" && $6 ~ /^rw(,|$)/ { found = 1 } END { exit found ? 0 : 1 }' "$WORK/mountinfo.txt" || fail "/data/world is not mounted rw"
ok "process $pid runs as uid/gid 65532 (real, effective, saved, fs), root mount ro, /data/world rw, no capabilities, no-new-privileges"

step "behavioural read-only and volume check from an ephemeral container (restricted profile, UID 65532)"
cat > "$WORK/debug-custom.json" <<'JSON'
{"securityContext":{"runAsUser":65532,"runAsGroup":65532}}
JSON
k debug pod/gb-engine-0 --quiet --container=gbprobe --target=gb-server --image="$BUSYBOX" --profile=restricted --custom="$WORK/debug-custom.json" -- \
  sh -c 'grep "^Uid:" /proc/1/status; if touch /proc/1/root/gb-ci-probe 2>/dev/null; then rm -f /proc/1/root/gb-ci-probe; echo ROOTFS=writable; exit 1; fi; echo ROOTFS=readonly; touch /proc/1/root/data/world/.gb-ci-probe && rm /proc/1/root/data/world/.gb-ci-probe && echo DATA=writable; ls -l /proc/1/root/data/world'
probe_exit() { k get pod gb-engine-0 -o json | jq -e '.status.ephemeralContainerStatuses[]? | select(.name=="gbprobe") | .state.terminated.exitCode' >/dev/null; }
wait_for 120 "the gbprobe ephemeral container to terminate" probe_exit || fail "gbprobe did not terminate"
probe_rc=$(k get pod gb-engine-0 -o json | jq -r '.status.ephemeralContainerStatuses[] | select(.name=="gbprobe") | .state.terminated.exitCode')
k logs pod/gb-engine-0 -c gbprobe > "$WORK/gbprobe.log" || true
cat "$WORK/gbprobe.log"
[ "$probe_rc" = "0" ] || fail "gbprobe exited $probe_rc"
grep -q '^Uid:.65532.65532.65532.65532' "$WORK/gbprobe.log" || fail "gbprobe did not see uid 65532"
grep -q '^ROOTFS=readonly$' "$WORK/gbprobe.log" || fail "root filesystem of the server is writable"
grep -q '^DATA=writable$' "$WORK/gbprobe.log" || fail "/data/world is not writable"
grep -q 'gb.db' "$WORK/gbprobe.log" || fail "gb.db not present in /data/world"
ok "ephemeral probe: uid 65532, root EROFS, /data/world writable, gb.db present"

# ------------------------------------------------------------ admin path --
step "probes and status through port-forward (before initialisation)"
start_port_forward
curl -fsS "http://127.0.0.1:$PLAYER_PORT/readyz" > "$WORK/readyz-1.json"
[ "$(jq -r .mode "$WORK/readyz-1.json")" = "UNINITIALIZED" ] && [ "$(jq -r .game_ready "$WORK/readyz-1.json")" = "false" ] || fail "readyz: $(cat "$WORK/readyz-1.json")"
curl -fsS "http://127.0.0.1:$PLAYER_PORT/livez" >/dev/null || fail "/livez"
curl -fsS "http://127.0.0.1:$PLAYER_PORT/status" > "$WORK/status-1.json"
[ "$(jq -r .stars "$WORK/status-1.json")" = "0" ] || fail "fresh store already has stars: $(cat "$WORK/status-1.json")"
[ "$(curl -s -o /dev/null -w '%{http_code}' "http://127.0.0.1:$ADMIN_PORT/admin/v1/status")" = "401" ] || fail "admin without token is not 401"
ok "UNINITIALIZED, game_ready=false, livez 200, 0 stars, admin refuses without a token"

step "gbctl through the port-forward: wrong token, init once, status, second init refused"
set +e
docker run --rm --network host -e GBCTL_SERVER="http://127.0.0.1:$ADMIN_PORT" -e GBCTL_TOKEN=wrong-token-0123456789 gbctl:ci status >/dev/null 2>&1
rc=$?
set -e
[ "$rc" -eq 3 ] || fail "wrong token exit $rc, expected 3"
gbctl -json universe init -seed 4242 -stars 6 -min-planets 1 -max-planets 3 > "$WORK/init.json" || { cat "$WORK/init.json"; fail "universe init failed"; }
[ "$(jq -r .result.seed "$WORK/init.json")" = "4242" ] && [ "$(jq -r .result.stars "$WORK/init.json")" = "6" ] || fail "init record: $(cat "$WORK/init.json")"
gbctl -json status > "$WORK/status-2.json"
[ "$(jq -r .status.mode "$WORK/status-2.json")" = "SERVING" ] || fail "mode after init: $(jq -r .status.mode "$WORK/status-2.json")"
[ "$(jq -r .status.stars "$WORK/status-2.json")" = "6" ] && [ "$(jq -r .status.universe.seed "$WORK/status-2.json")" = "4242" ] || fail "status after init: $(cat "$WORK/status-2.json")"
next_update_1=$(jq -r .status.next_update "$WORK/status-2.json")
universe_1=$(jq -c .status.universe "$WORK/status-2.json")
set +e
gbctl universe init -seed 1 > "$WORK/reinit.out" 2>&1
rc=$?
set -e
[ "$rc" -eq 4 ] || { cat "$WORK/reinit.out"; fail "second init exit $rc, expected 4"; }
curl -fsS "http://127.0.0.1:$PLAYER_PORT/readyz" > "$WORK/readyz-2.json"
[ "$(jq -r .game_ready "$WORK/readyz-2.json")" = "true" ] || fail "readyz after init: $(cat "$WORK/readyz-2.json")"
ok "init: seed 4242, 6 stars, SERVING, next_update $next_update_1; second init exit 4; game_ready=true"

step "player listener: login and WebSocket paths answer (no race exists; a session is not exercised)"
code=$(curl -s -o "$WORK/login.out" -w '%{http_code}' -H 'Content-Type: application/json' -d '{"race":"nobody","password":"nobody-password","governor":0}' "http://127.0.0.1:$PLAYER_PORT/auth/login")
grep -qE '^4[0-9][0-9]$' <<<"$code" || fail "login of an unknown race returned $code: $(cat "$WORK/login.out")"
ws=$(curl -s -o /dev/null -w '%{http_code}' "http://127.0.0.1:$PLAYER_PORT/ws")
grep -qE '^4[0-9][0-9]$' <<<"$ws" || fail "WebSocket endpoint without a token returned $ws"
ok "login of an unknown race -> $code, WebSocket without token -> $ws (both refused, no server error)"

# ----------------------------------------------------------------- restart --
step "orderly restart (kubectl rollout restart), universe and schedule persist"
k logs -f pod/gb-engine-0 > "$WORK/gb-engine-0.before-restart.log" 2>&1 &
LOG_PID=$!
sleep 1
k rollout restart statefulset/gb-engine
k rollout status statefulset/gb-engine --timeout=300s
k wait pod/gb-engine-0 --for=condition=Ready --timeout=120s
sleep 1
kill "$LOG_PID" 2>/dev/null || true
LOG_PID=""
pod_uid_2=$(k get pod gb-engine-0 -o jsonpath='{.metadata.uid}')
[ "$pod_uid_1" != "$pod_uid_2" ] || fail "pod was not replaced"
grep -q 'shutdown complete' "$WORK/gb-engine-0.before-restart.log" || { tail -30 "$WORK/gb-engine-0.before-restart.log"; fail "the replaced pod did not log an orderly shutdown"; }
[ "$(k get pod gb-engine-0 -o jsonpath='{.status.containerStatuses[0].restartCount}')" = "0" ] || fail "new pod has restarts"
start_port_forward
gbctl -json status > "$WORK/status-3.json"
[ "$(jq -r .status.mode "$WORK/status-3.json")" = "SERVING" ] || fail "mode after restart: $(cat "$WORK/status-3.json")"
[ "$(jq -c .status.universe "$WORK/status-3.json")" = "$universe_1" ] || fail "universe record changed across restart: $universe_1 -> $(jq -c .status.universe "$WORK/status-3.json")"
[ "$(jq -r .status.next_update "$WORK/status-3.json")" = "$next_update_1" ] || fail "schedule changed across restart: $next_update_1 -> $(jq -r .status.next_update "$WORK/status-3.json")"
[ "$(jq -r .status.stars "$WORK/status-3.json")" = "6" ] && [ "$(jq -r .status.num_updates "$WORK/status-3.json")" = "0" ] || fail "world after restart: $(cat "$WORK/status-3.json")"
curl -fsS "http://127.0.0.1:$PLAYER_PORT/readyz" | jq -e '.game_ready == true' >/dev/null || fail "readyz after restart"
ok "new pod $pod_uid_2 (old $pod_uid_1 shut down orderly); same universe record and next_update $next_update_1; game_ready=true"

jq -n --arg cluster "kind-$CLUSTER" --arg server "$server_version" --arg image "$expected_config" --arg nu "$next_update_1" --argjson universe "$universe_1" \
  '{kind: "gb-kind-e2e-record", cluster: $cluster, server_version: $server, image_config_digest: $image, universe: $universe, next_update: $nu,
    proves: ["restricted PSS admission", "uid 65532", "read-only root", "writable volume", "probes", "gbctl init once then exit 4", "orderly restart keeps universe and schedule"],
    does_not_prove: ["Longhorn RWOP", "NetworkPolicy enforcement", "Traefik", "PodMonitor", "node fencing", "player session"]}' > "$WORK/kind-e2e-record.json"
cat "$WORK/kind-e2e-record.json"
if [ -n "${GITHUB_STEP_SUMMARY:-}" ]; then
  { echo "## kind e2e"; echo '```json'; cat "$WORK/kind-e2e-record.json"; echo '```'; } >> "$GITHUB_STEP_SUMMARY"
fi
FAILED=0
