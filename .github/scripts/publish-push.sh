#!/usr/bin/env bash
# Push the scanned OCI archives to GHCR with their digests preserved, verify
# them, tag master, and write a validated publish record (Phase 2C brief
# section 9; ADR-0006; GB2C-01). Usage:
#
#   publish-push.sh <images-dir> <record.json>
#
# Environment: IMAGE_NAMESPACE (ghcr.io/phreddrick), GITHUB_SHA,
# GITHUB_SERVER_URL, GITHUB_REPOSITORY, GITHUB_RUN_ID, GITHUB_RUN_ATTEMPT,
# SCAN_DIR (Trivy reports already evaluated), SOURCE_DATE_EPOCH, and a
# `crane` on PATH logged in to the registry (docker/login-action). Outputs
# server_digest and gbctl_digest to $GITHUB_OUTPUT only after a validated
# record says status=published.
#
# Why crane: `crane push <oci-layout-dir> <ref>` uploads the manifest bytes
# exactly as the archive holds them, so the published digest is the digest
# the scan and the record refer to. Each archive is pushed under the commit
# tag, read back by digest, its config checked for this commit's revision
# label; only when both images verified are both tagged `master`.
#
# Record and outcome (publish_record.py is the contract):
#   published   both images pushed, verified by digest, both tagged master,
#               and the record finalised and re-validated at <record.json>.
#               "publish-push: ALL PASS", exit 0, digests in GITHUB_OUTPUT.
#   partial     something reached the registry and the run did not end with
#               a finalised `published` record (two GHCR pushes are not one
#               transaction; a record that cannot be finalised is the same
#               outcome even when both images verified). The record, or the
#               kept temporary file, names every identifier that reached the
#               registry. "publish-push: PARTIAL_PUBLICATION", exit 1, no
#               outputs, so the digest pull request is never opened.
#   failed      nothing was pushed. "publish-push: FAILED", exit 1.
# Record finalisation (GB2C-01, GB2C-01R) is a verified operation: the
# destination must not exist at all when the script starts (a directory, a
# symlink or an earlier record is refused before any push; a contradictory
# record is never overwritten); the record is rendered from publish-record.jq
# into a temporary file in the same directory, validated, hashed, moved with
# `mv -T` (exact destination: a directory can never become a container for
# it), then the destination is required to be a regular non-symlink file at
# the exact pathname whose SHA-256 equals the validated temporary file and
# which publish_record.py accepts again on a fresh read. Only then are the
# digests exported and ALL PASS printed. The jq program is compiled once
# before any push, so it cannot fail for the first time after an image is on
# the registry. No step converts an error into success: mv, cat, jq, sha256sum
# and the validator are all checked.
set -euo pipefail

IMAGES_DIR=${1:?images dir}
RECORD_OUT=${2:?record path}
HERE=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
: "${IMAGE_NAMESPACE:?}" "${GITHUB_SHA:?}" "${GITHUB_REPOSITORY:?}" "${GITHUB_SERVER_URL:?}" "${GITHUB_RUN_ID:?}"
for tool in crane jq python3 tar; do
  command -v "$tool" >/dev/null || { echo "publish-push: $tool is required" >&2; exit 2; }
done
JQ_PROGRAM="$HERE/publish-record.jq"
VALIDATOR="$HERE/publish_record.py"
[ -f "$JQ_PROGRAM" ] && [ -f "$VALIDATOR" ] || { echo "publish-push: publish-record.jq / publish_record.py missing next to this script" >&2; exit 2; }
for tool in mv sha256sum; do
  command -v "$tool" >/dev/null || { echo "publish-push: $tool is required" >&2; exit 2; }
done
RECORD_DIR=$(dirname "$RECORD_OUT")
[ -d "$RECORD_DIR" ] && [ -w "$RECORD_DIR" ] || { echo "publish-push: record directory $RECORD_DIR is not a writable directory" >&2; exit 2; }
if [ -L "$RECORD_OUT" ] || [ -e "$RECORD_OUT" ]; then
  what="exists"; [ -L "$RECORD_OUT" ] && what="is a symlink"; [ -d "$RECORD_OUT" ] && what="is a directory"
  echo "publish-push: record path $RECORD_OUT already $what; refusing to publish (an existing publication record is never overwritten or extended)" >&2
  exit 2
fi
RUN_URL="$GITHUB_SERVER_URL/$GITHUB_REPOSITORY/actions/runs/$GITHUB_RUN_ID/attempts/${GITHUB_RUN_ATTEMPT:-1}"
TRIVY_LINE=$(head -1 "${SCAN_DIR:-/nonexistent}/trivy-version.txt" 2>/dev/null || true)

declare -A LOCAL=() REMOTE=() CONFIG=() SIZE=() STATE=()
for t in gb-server gbctl; do STATE[$t]=not_pushed; LOCAL[$t]=""; REMOTE[$t]=""; CONFIG[$t]=""; SIZE[$t]=0; done
STATUS=started          # started | published | partial | failed
TAGGED_MASTER=false
WORK=""

render_record() { # render_record <status> > stdout; fails when jq fails
  jq -n -f "$JQ_PROGRAM" \
    --arg status "$1" --arg master "$TAGGED_MASTER" --arg sha "$GITHUB_SHA" --arg run "$RUN_URL" \
    --arg epoch "${SOURCE_DATE_EPOCH:-}" --arg ns "$IMAGE_NAMESPACE" --arg trivy "$TRIVY_LINE" \
    --arg s_state "${STATE[gb-server]}" --arg s_local "${LOCAL[gb-server]}" --arg s_remote "${REMOTE[gb-server]}" --arg s_config "${CONFIG[gb-server]}" --arg s_size "${SIZE[gb-server]}" \
    --arg c_state "${STATE[gbctl]}" --arg c_local "${LOCAL[gbctl]}" --arg c_remote "${REMOTE[gbctl]}" --arg c_config "${CONFIG[gbctl]}" --arg c_size "${SIZE[gbctl]}"
}

# Compile and render the record program once, before anything is pushed.
render_record started > /dev/null || { echo "publish-push: the record program $JQ_PROGRAM does not compile or render; refusing to publish" >&2; exit 2; }

published_summary() { # names everything that reached the registry
  local t out=""
  for t in gb-server gbctl; do
    case "${STATE[$t]}" in
      verified|pushed_unverified) out+=" $IMAGE_NAMESPACE/$t:$GITHUB_SHA (${STATE[$t]}, digest ${REMOTE[$t]:-unknown})";;
    esac
  done
  echo "${out:- nothing}"
}

final_status() {
  local t any_pushed=0
  if [ "$STATUS" = published ]; then echo published; return; fi
  for t in gb-server gbctl; do
    case "${STATE[$t]}" in verified|pushed_unverified) any_pushed=1;; esac
  done
  if [ "$any_pushed" -eq 1 ]; then echo partial; else echo failed; fi
}

finalise_record() { # finalise_record <status> <tmp>; prints FAIL lines; returns 0 only when every check passed
  local status=$1 tmp=$2 before after
  if [ -L "$RECORD_OUT" ] || [ -e "$RECORD_OUT" ]; then
    echo "FAIL publish record destination $RECORD_OUT appeared during the run ($( [ -L "$RECORD_OUT" ] && echo symlink || { [ -d "$RECORD_OUT" ] && echo directory || echo existing path; } )); refusing to overwrite it; the validated record is kept at $tmp" >&2
    return 1
  fi
  if ! : > "$tmp"; then
    echo "FAIL cannot create the temporary record $tmp (record directory not writable?)" >&2
    return 1
  fi
  if ! render_record "$status" > "$tmp"; then
    echo "FAIL publish record could not be rendered (jq failed)" >&2
    return 1
  fi
  if ! python3 "$VALIDATOR" "$tmp" --expect-status "$status" --expect-commit "$GITHUB_SHA" --expect-namespace "$IMAGE_NAMESPACE" --images-dir "$IMAGES_DIR"; then
    echo "FAIL publish record failed validation; it is kept at $tmp for inspection and is NOT the record" >&2
    return 1
  fi
  before=$(sha256sum "$tmp" | cut -d' ' -f1) || { echo "FAIL cannot hash the validated record $tmp" >&2; return 1; }
  [ -n "$before" ] || { echo "FAIL empty hash for $tmp" >&2; return 1; }
  if ! mv -T "$tmp" "$RECORD_OUT"; then
    echo "FAIL publish record could not be moved to $RECORD_OUT (mv -T failed); the validated record is kept at $tmp if it still exists" >&2
    return 1
  fi
  if [ -L "$RECORD_OUT" ]; then
    echo "FAIL $RECORD_OUT is a symlink after the move; not a publication record" >&2
    return 1
  fi
  if [ ! -f "$RECORD_OUT" ]; then
    echo "FAIL publish record is missing at the exact pathname $RECORD_OUT after the move (mv exited 0)" >&2
    return 1
  fi
  after=$(sha256sum "$RECORD_OUT" | cut -d' ' -f1) || { echo "FAIL cannot hash the finalised record $RECORD_OUT" >&2; return 1; }
  if [ "$after" != "$before" ]; then
    echo "FAIL finalised record $RECORD_OUT (sha256 $after) differs from the validated record (sha256 $before)" >&2
    return 1
  fi
  if ! python3 "$VALIDATOR" "$RECORD_OUT" --expect-status "$status" --expect-commit "$GITHUB_SHA" --expect-namespace "$IMAGE_NAMESPACE" --images-dir "$IMAGES_DIR" > /dev/null; then
    echo "FAIL finalised record $RECORD_OUT does not validate on a fresh read" >&2
    return 1
  fi
  if ! RECORD_TEXT=$(cat "$RECORD_OUT"); then
    echo "FAIL finalised record $RECORD_OUT cannot be read back" >&2
    return 1
  fi
  return 0
}

on_exit() {
  rc=$?
  trap - EXIT
  set +e
  STATUS=$(final_status)
  RECORD_TEXT=""
  tmp="$RECORD_DIR/.$(basename "$RECORD_OUT").tmp.$$"
  record_ok=0
  if finalise_record "$STATUS" "$tmp"; then record_ok=1; fi
  if [ "$record_ok" -eq 1 ]; then
    echo "publish record ($RECORD_OUT, sha256 $(sha256sum "$RECORD_OUT" | cut -d' ' -f1)):"; printf '%s\n' "$RECORD_TEXT"
    if [ -n "${GITHUB_STEP_SUMMARY:-}" ]; then { echo "## Publish record ($STATUS)"; echo '```json'; printf '%s\n' "$RECORD_TEXT"; echo '```'; } >> "$GITHUB_STEP_SUMMARY"; fi
  else
    # Never remove $RECORD_OUT here: this script creates it only through a
    # verified move, so anything there was not written by this run.
    if [ -n "${GITHUB_STEP_SUMMARY:-}" ]; then { echo "## Publish record NOT FINALISED (status $STATUS)"; echo "Published:$(published_summary)"; } >> "$GITHUB_STEP_SUMMARY"; fi
  fi
  [ -n "$WORK" ] && rm -rf "$WORK"
  if [ "$rc" -eq 0 ] && [ "$STATUS" = published ] && [ "$record_ok" -eq 1 ]; then
    if [ -n "${GITHUB_OUTPUT:-}" ]; then
      { echo "server_digest=${REMOTE[gb-server]}"; echo "gbctl_digest=${REMOTE[gbctl]}"; } >> "$GITHUB_OUTPUT" || { echo "FAIL could not write GITHUB_OUTPUT" >&2; exit 1; }
    fi
    echo "publish-push: ALL PASS"
    exit 0
  fi
  case "$STATUS" in
    partial)
      echo "publish-push: PARTIAL_PUBLICATION (exit $rc, record_ok=$record_ok). On the registry:$(published_summary). Not a release: no digest pull request, no master tag unless both verified. Operator procedure: docs/operations/bootstrap.md section 2 (partial publication)." >&2;;
    published)
      echo "publish-push: PARTIAL_PUBLICATION (registry writes complete: both images verified and tagged master, but the publication record was NOT finalised; not reporting success, no digests exported). On the registry:$(published_summary). Operator procedure: docs/operations/bootstrap.md section 2 (partial publication)." >&2;;
    *)
      echo "publish-push: FAILED (status=$STATUS, exit $rc, record_ok=$record_ok); nothing was pushed." >&2;;
  esac
  exit 1
}
trap on_exit EXIT
fail() { echo "FAIL $*" >&2; exit 1; }
ok() { echo "PASS $*"; }

WORK=$(mktemp -d)
# Archive identities first, for both images, so a bad second archive is found
# before the first image is pushed.
for t in gb-server gbctl; do
  archive="$IMAGES_DIR/$t.oci.tar"
  rec=$(python3 "$HERE/oci_digest.py" "$archive") || fail "$archive: not a valid single-manifest OCI archive"
  LOCAL[$t]=$(jq -r .manifest_digest <<<"$rec")
  CONFIG[$t]=$(jq -r .config_digest <<<"$rec")
  SIZE[$t]=$(jq -r .compressed_size <<<"$rec")
  [[ "${LOCAL[$t]}" =~ ^sha256:[0-9a-f]{64}$ ]] || fail "$t: archive manifest digest malformed: ${LOCAL[$t]}"
  layout="$WORK/$t"
  mkdir -p "$layout"
  tar -xf "$archive" -C "$layout" || fail "$archive: cannot extract"
  [ -f "$layout/index.json" ] || fail "$archive is not an OCI layout"
done
[ "${LOCAL[gb-server]}" != "${LOCAL[gbctl]}" ] || fail "both archives have the same manifest digest; they are not two images"
ok "archives: gb-server ${LOCAL[gb-server]}, gbctl ${LOCAL[gbctl]}"

for t in gb-server gbctl; do
  ref="$IMAGE_NAMESPACE/$t:$GITHUB_SHA"
  layout="$WORK/$t"
  echo "pushing $IMAGES_DIR/$t.oci.tar -> $ref"
  if ! crane push "$layout" "$ref" >/dev/null; then
    STATE[$t]=push_failed
    fail "$t: crane push to $ref failed"
  fi
  # From here the bytes may be on the registry: record them whatever happens next.
  STATE[$t]=pushed_unverified
  REMOTE[$t]=$(crane digest "$ref") || fail "$t: pushed but 'crane digest $ref' failed; the registry state is unknown"
  [ "${REMOTE[$t]}" = "${LOCAL[$t]}" ] || fail "$t: published digest ${REMOTE[$t]} differs from the scanned archive ${LOCAL[$t]}"
  crane manifest "$IMAGE_NAMESPACE/$t@${REMOTE[$t]}" > "$WORK/$t.manifest.json" || fail "$t: manifest not retrievable by digest ${REMOTE[$t]}"
  [ "$(jq -r .mediaType "$WORK/$t.manifest.json")" = "application/vnd.oci.image.manifest.v1+json" ] || fail "$t: published manifest is not a single OCI image manifest"
  crane config "$IMAGE_NAMESPACE/$t@${REMOTE[$t]}" > "$WORK/$t.config.json" || fail "$t: config not retrievable"
  [ "$(jq -r '.config.Labels["org.opencontainers.image.revision"]' "$WORK/$t.config.json")" = "$GITHUB_SHA" ] || fail "$t: published config revision label is not $GITHUB_SHA"
  [ "$(jq -r '.os + "/" + .architecture' "$WORK/$t.config.json")" = "linux/amd64" ] || fail "$t: published platform"
  [ "$(jq -r '.config.User' "$WORK/$t.config.json")" = "65532:65532" ] || fail "$t: published user"
  STATE[$t]=verified
  ok "$t published as $ref, retrievable by digest ${REMOTE[$t]} (equals the scanned archive), revision label $GITHUB_SHA"
done

for t in gb-server gbctl; do
  crane tag "$IMAGE_NAMESPACE/$t@${REMOTE[$t]}" master >/dev/null || fail "$t: tagging master failed"
  [ "$(crane digest "$IMAGE_NAMESPACE/$t:master")" = "${REMOTE[$t]}" ] || fail "$t: master tag does not resolve to ${REMOTE[$t]}"
done
TAGGED_MASTER=true
ok "both images tagged master (convenience only; manifests reference digests)"
STATUS=published
