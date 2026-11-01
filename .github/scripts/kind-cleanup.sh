#!/usr/bin/env bash
# Delete the disposable kind cluster that THIS job created, and prove it is
# gone (GB2C-04, GB2C-04R). Used by kind-e2e.sh from its exit trap and by the
# workflow's `if: always()` fallback step; both paths share one ownership
# contract and one fail-closed verdict.
#
#   kind-cleanup.sh --ownership FILE [--cluster NAME] [--kubeconfig PATH]
#                   [--main-status N] [--timeout SECONDS] [--fallback]
#
# Ownership contract. The only authority to delete a cluster is the ownership
# marker that kind-e2e.sh writes, in a runner-local file under the job's own
# temporary directory, immediately after `kind create cluster` exited 0 and
# never before. The marker is a JSON object:
#   {"kind":"gb-kind-e2e-ownership","state":"owned","cluster":NAME,
#    "repository":..,"run_id":..,"run_attempt":..,"job":..,"created_at":..}
# Before any deletion this script requires: the marker is a regular,
# non-symlink file that parses as that object; state is "owned"; the cluster
# name matches ^gb-ci[a-z0-9-]*$ (63 chars at most) and, when --cluster is
# given, equals it; repository, run_id, run_attempt and job equal this
# process's GITHUB_REPOSITORY, GITHUB_RUN_ID, GITHUB_RUN_ATTEMPT and
# GITHUB_JOB. Nothing is ever derived from a name prefix or from a cluster
# listing: an existing cluster without a valid marker from this run is left
# alone (hosted runners are disposable; deleting what this job cannot prove it
# created is not). After confirmed absence the marker is rewritten with
# state "released", so a later invocation (the workflow fallback) has nothing
# to delete and says so.
#
# Four things are kept apart and each reported on its own line:
#   1. the deletion request (`kind delete cluster`), 2. its exit status,
#   3. an independent inspection (`kind get clusters`, `docker ps -a`) that
#   must itself succeed, 4. the confirmed absence of the cluster and of its
#   node containers. An inspection that fails is a cleanup failure, never
#   evidence of absence. Nothing here is `|| true`.
#
# --fallback (the workflow step): a missing marker means this job never
# recorded a creation (precondition failure, refused or failed creation, or
# a cleanup that already released); nothing is deleted and the step passes,
# because the main step already carries that failure. A marker that exists
# but is malformed or belongs to another run, attempt, job, repository or
# cluster is refused with a non-zero exit and nothing is deleted. A marker
# in state "released" means the script's own cleanup already confirmed
# absence; nothing is deleted. With a valid "owned" marker the cluster is
# deleted only if it is still listed (or if the listing fails), then the
# inspection and verdict are the same as in the trap path.
# Without --fallback (the trap path) a missing or invalid marker is a refusal
# (exit 2): the trap must never reach this script without one.
#
# --main-status N: the exit status of the test that ran before cleanup; it is
# preserved. Exit status: 0 only when the main test passed (or none was
# reported) AND cleanup is confirmed; N when the main test failed (whether or
# not cleanup also failed, both are printed); 3 when the main test passed but
# cleanup failed; 2 when ownership is refused. "kind-e2e: ALL PASS" is
# printed only for exit 0 in the trap path; the fallback step never prints
# it (the main step's verdict stands).
#
# Safety: no deletion without a valid marker from this run; every command is
# bounded by `timeout`; no kubeconfig is read except to pass it to kind, no
# kubectl call is made, and nothing but `kind delete cluster` deletes.
set -euo pipefail

CLUSTER_ARG=""
OWNERSHIP=""
KUBECONFIG_PATH=""
MAIN_STATUS=0
TIMEOUT=300
FALLBACK=0
while [ $# -gt 0 ]; do
  case "$1" in
    --cluster) CLUSTER_ARG=${2:?}; shift 2;;
    --ownership) OWNERSHIP=${2:?}; shift 2;;
    --kubeconfig) KUBECONFIG_PATH=${2:?}; shift 2;;
    --main-status) MAIN_STATUS=${2:?}; shift 2;;
    --timeout) TIMEOUT=${2:?}; shift 2;;
    --fallback) FALLBACK=1; shift;;
    *) echo "kind-cleanup: unknown argument $1" >&2; exit 2;;
  esac
done
refuse() { echo "REFUSED cleanup: $*" >&2; echo "kind-cleanup: nothing deleted" >&2; exit 2; }
[ -n "$OWNERSHIP" ] || refuse "--ownership FILE is required; deletion is authorised only by this job's ownership marker"
[[ "$MAIN_STATUS" =~ ^[0-9]+$ ]] || refuse "--main-status must be an integer"
[[ "$TIMEOUT" =~ ^[0-9]+$ ]] || refuse "--timeout must be an integer"
if [ -n "$CLUSTER_ARG" ]; then
  [[ "$CLUSTER_ARG" =~ ^gb-ci[a-z0-9-]*$ ]] && [ "${#CLUSTER_ARG}" -le 63 ] || refuse "cluster name '$CLUSTER_ARG' is not a disposable gb-ci* name"
fi
for tool in kind docker timeout jq; do
  command -v "$tool" >/dev/null || refuse "$tool is required"
done
for v in GITHUB_REPOSITORY GITHUB_RUN_ID GITHUB_RUN_ATTEMPT GITHUB_JOB; do
  [ -n "${!v:-}" ] || refuse "$v is not set; the run identity cannot be compared with the ownership marker"
done

mode=""; [ "$FALLBACK" -eq 1 ] && mode=" (fallback)"
echo "== cleanup$mode: ownership marker $OWNERSHIP, main test status $MAIN_STATUS"

# ------------------------------------------------------- ownership marker --
if [ -L "$OWNERSHIP" ]; then
  refuse "ownership marker $OWNERSHIP is a symlink"
fi
if [ ! -e "$OWNERSHIP" ]; then
  if [ "$FALLBACK" -eq 1 ]; then
    echo "NOTE cleanup: no ownership marker at $OWNERSHIP: this job recorded no cluster creation (precondition failure, refused or failed creation, or an already released cleanup); nothing to delete"
    if [ "$MAIN_STATUS" -eq 0 ]; then echo "kind-cleanup: nothing owned, nothing deleted"; exit 0; fi
    echo "kind-e2e: FAILED (test exit $MAIN_STATUS); nothing owned, nothing deleted" >&2
    exit "$MAIN_STATUS"
  fi
  refuse "ownership marker $OWNERSHIP does not exist"
fi
[ -f "$OWNERSHIP" ] || refuse "ownership marker $OWNERSHIP is not a regular file"
[ -s "$OWNERSHIP" ] || refuse "ownership marker $OWNERSHIP is malformed: empty file"
marker=$(jq -c '
  if type != "object" then error("marker is not a JSON object") else . end
  | if .kind != "gb-kind-e2e-ownership" then error("marker kind is not gb-kind-e2e-ownership") else . end
  | if (.state | type) != "string" or ((.state == "owned" or .state == "released") | not) then error("marker state is not owned/released") else . end
  | if (.cluster | type) != "string" or (.cluster | test("^gb-ci[a-z0-9-]*$") | not) or (.cluster | length) > 63 then error("marker cluster name is not a disposable gb-ci* name") else . end
  | if (.repository | type) != "string" or (.run_id | type) != "string" or (.run_attempt | type) != "string" or (.job | type) != "string" then error("marker identity fields missing") else . end
  | {state, cluster, repository, run_id, run_attempt, job}' "$OWNERSHIP" 2>&1) || refuse "ownership marker $OWNERSHIP is malformed: $marker"
[ "$(wc -l <<<"$marker")" -eq 1 ] && [ -n "$marker" ] || refuse "ownership marker $OWNERSHIP is malformed: expected exactly one JSON object"
m_state=$(jq -r .state <<<"$marker")
m_cluster=$(jq -r .cluster <<<"$marker")
m_repo=$(jq -r .repository <<<"$marker")
m_run=$(jq -r .run_id <<<"$marker")
m_attempt=$(jq -r .run_attempt <<<"$marker")
m_job=$(jq -r .job <<<"$marker")
[ "$m_repo" = "$GITHUB_REPOSITORY" ] || refuse "ownership marker is for repository '$m_repo', this run is '$GITHUB_REPOSITORY'"
[ "$m_run" = "$GITHUB_RUN_ID" ] || refuse "ownership marker is for run '$m_run', this run is '$GITHUB_RUN_ID'"
[ "$m_attempt" = "$GITHUB_RUN_ATTEMPT" ] || refuse "ownership marker is for attempt '$m_attempt', this attempt is '$GITHUB_RUN_ATTEMPT'"
[ "$m_job" = "$GITHUB_JOB" ] || refuse "ownership marker is for job '$m_job', this job is '$GITHUB_JOB'"
if [ -n "$CLUSTER_ARG" ] && [ "$CLUSTER_ARG" != "$m_cluster" ]; then
  refuse "ownership marker names cluster '$m_cluster', not the requested '$CLUSTER_ARG'"
fi
CLUSTER=$m_cluster
if [ "$m_state" = "released" ]; then
  if [ "$FALLBACK" -eq 1 ]; then
    echo "NOTE cleanup: marker is released: this job's cleanup already confirmed that $CLUSTER is absent; nothing to delete"
    if [ "$MAIN_STATUS" -eq 0 ]; then echo "kind-cleanup: already released, nothing deleted"; exit 0; fi
    echo "kind-e2e: FAILED (test exit $MAIN_STATUS); cleanup already released" >&2
    exit "$MAIN_STATUS"
  fi
  refuse "ownership marker for $CLUSTER is already released"
fi
echo "PASS ownership: marker $OWNERSHIP is owned by this run ($GITHUB_REPOSITORY run $GITHUB_RUN_ID attempt $GITHUB_RUN_ATTEMPT job $GITHUB_JOB) for cluster $CLUSTER"

CLEANUP_OK=1
problem() { echo "FAIL cleanup: $*" >&2; CLEANUP_OK=0; }
ok() { echo "PASS cleanup: $*"; }

list_clusters() { # prints the listing on stdout; returns kind's exit status
  local out err rc
  err=$(mktemp)
  out=$(timeout "$TIMEOUT" kind get clusters 2>"$err"); rc=$?
  if [ "$rc" -ne 0 ]; then echo "kind get clusters exited $rc: $(tr '\n' ' ' <"$err")" >&2; fi
  rm -f "$err"
  printf '%s\n' "$out"
  return "$rc"
}

# 1 + 2: deletion request and its exit status.
do_delete=1
if [ "$FALLBACK" -eq 1 ]; then
  if listing=$(list_clusters); then
    if grep -qx "$CLUSTER" <<<"$listing"; then
      echo "cluster $CLUSTER is still listed; the script's own cleanup did not release it"
    else
      do_delete=0
      ok "cluster $CLUSTER not listed before fallback deletion (listing succeeded)"
    fi
  else
    problem "cannot inspect kind clusters before the fallback deletion; deleting the owned cluster anyway"
  fi
fi
if [ "$do_delete" -eq 1 ]; then
  delete_args=(delete cluster --name "$CLUSTER")
  [ -n "$KUBECONFIG_PATH" ] && delete_args+=(--kubeconfig "$KUBECONFIG_PATH")
  delete_rc=0
  timeout "$TIMEOUT" kind "${delete_args[@]}" || delete_rc=$?
  if [ "$delete_rc" -eq 0 ]; then
    ok "deletion request accepted (kind delete cluster exited 0)"
  else
    problem "kind delete cluster exited $delete_rc (timeout ${TIMEOUT}s)"
  fi
fi

# 3 + 4: independent inspection, which must succeed, then confirmed absence.
if listing=$(list_clusters); then
  if grep -qx "$CLUSTER" <<<"$listing"; then
    problem "cluster $CLUSTER is still listed after deletion"
  else
    ok "cluster $CLUSTER absent (confirmed by a successful listing)"
  fi
else
  problem "cannot inspect kind clusters after deletion; absence is NOT confirmed"
fi
containers_err=$(mktemp)
ps_rc=0
containers=$(timeout "$TIMEOUT" docker ps -a --filter "name=${CLUSTER}-" --format '{{.Names}}' 2>"$containers_err") || ps_rc=$?
if [ "$ps_rc" -ne 0 ]; then
  problem "docker ps exited $ps_rc ($(tr '\n' ' ' <"$containers_err")); container absence is NOT confirmed"
elif [ -n "$containers" ]; then
  problem "node containers remain: $(tr '\n' ' ' <<<"$containers")"
else
  ok "no node container named ${CLUSTER}-* remains (docker ps succeeded)"
fi
rm -f "$containers_err"
[ -n "$KUBECONFIG_PATH" ] && rm -f "$KUBECONFIG_PATH"

# Release the marker only after confirmed absence; a failed cleanup keeps it
# "owned" so the fallback step may retry the deletion of the same cluster.
if [ "$CLEANUP_OK" -eq 1 ]; then
  released_tmp="$OWNERSHIP.released.$$"
  if jq --arg at "$(date -u +%Y-%m-%dT%H:%M:%SZ)" '.state = "released" | .released_at = $at' "$OWNERSHIP" > "$released_tmp" && mv -T "$released_tmp" "$OWNERSHIP"; then
    ok "ownership marker released ($OWNERSHIP)"
  else
    rm -f "$released_tmp"
    problem "could not release the ownership marker $OWNERSHIP"
  fi
else
  echo "ownership marker kept as owned at $OWNERSHIP (cleanup not confirmed)"
fi

if [ "$MAIN_STATUS" -eq 0 ] && [ "$CLEANUP_OK" -eq 1 ]; then
  if [ "$FALLBACK" -eq 1 ]; then echo "kind-cleanup: fallback confirmed cluster $CLUSTER absent (the main step's own verdict stands)"; else echo "kind-e2e: ALL PASS"; fi
  exit 0
elif [ "$MAIN_STATUS" -ne 0 ] && [ "$CLEANUP_OK" -eq 1 ]; then
  echo "kind-e2e: FAILED (test exit $MAIN_STATUS); cleanup confirmed" >&2
  exit "$MAIN_STATUS"
elif [ "$MAIN_STATUS" -ne 0 ]; then
  echo "kind-e2e: FAILED (test exit $MAIN_STATUS) AND CLEANUP FAILED (cluster $CLUSTER may remain)" >&2
  exit "$MAIN_STATUS"
else
  echo "kind-e2e: TEST PASSED BUT CLEANUP FAILED (cluster $CLUSTER may remain); not a pass" >&2
  exit 3
fi
