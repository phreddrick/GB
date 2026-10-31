#!/usr/bin/env bash
# Checks on the two images the ci `docker` job just built, run in the hosted
# runner's Docker engine (Phase 2C brief section 5; ADR-0006). Nothing is
# pushed. Usage:
#
#   image-checks.sh <images-dir> <record.json>
#
# <images-dir> holds gb-server.oci.tar, gb-server.docker.tar, gbctl.oci.tar and
# gbctl.docker.tar as written by buildx (type=oci and type=docker exporters of
# the same build). Environment: EXPECTED_GO_VERSION (e.g. 1.27.3),
# SOURCE_DATE_EPOCH, GITHUB_SHA, GITHUB_SERVER_URL, GITHUB_REPOSITORY,
# GITHUB_RUN_ID, GITHUB_RUN_ATTEMPT (all set by the workflow).
#
# For each target it asserts: the OCI archive holds exactly one linux/amd64
# image manifest (digest recorded); the docker-loaded image's Id equals the
# archive's config digest (same artifact); config User 65532:65532 and the
# expected entrypoint; the rootfs contains the expected binary, no shell, no
# package manager, no credential-looking file, the other target's binary is
# absent (distinct images); the binary is a static ELF x86-64 built by the
# pinned Go toolchain with -trimpath, CGO_ENABLED=0 and no VCS stamp; running
# /bin/sh fails. The server image is then started read-only as 65532 with a
# tmpfs volume and must answer /readyz UNINITIALIZED, be administered through
# the gbctl image, stop cleanly on SIGTERM with exit 0, and refuse to start
# without secrets. Secrets are random, held in variables, never printed.
set -euo pipefail

IMAGES_DIR=${1:?images dir}
RECORD_OUT=${2:?record path}
HERE=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
: "${EXPECTED_GO_VERSION:?}"
WORK=$(mktemp -d)
trap 'rm -rf "$WORK"' EXIT

fail() { echo "FAIL $*" >&2; exit 1; }
ok() { echo "PASS $*"; }

declare -A BIN=([gb-server]=/usr/local/bin/gb-server [gbctl]=/usr/local/bin/gbctl)
declare -A OTHER=([gb-server]=/usr/local/bin/gbctl [gbctl]=/usr/local/bin/gb-server)
declare -A MANIFEST CONFIG SIZE TARSIZE GOVER

for t in gb-server gbctl; do
  oci="$IMAGES_DIR/$t.oci.tar"; dock="$IMAGES_DIR/$t.docker.tar"; tag="$t:ci"
  [ -s "$oci" ] || fail "$oci missing"
  [ -s "$dock" ] || fail "$dock missing"

  # 1. Archive identity (verified digests, single manifest, platform).
  rec=$(python3 "$HERE/oci_digest.py" "$oci")
  MANIFEST[$t]=$(jq -r .manifest_digest <<<"$rec")
  CONFIG[$t]=$(jq -r .config_digest <<<"$rec")
  TARSIZE[$t]=$(stat -c %s "$oci")
  [ "$(jq -r '.platform.os + "/" + .platform.architecture' <<<"$rec")" = "linux/amd64" ] || fail "$t platform is not linux/amd64: $rec"
  [ "$(jq -r .user <<<"$rec")" = "65532:65532" ] || fail "$t config User is not 65532:65532"
  [ "$(jq -r '.entrypoint | join(" ")' <<<"$rec")" = "${BIN[$t]}" ] || fail "$t entrypoint is not ${BIN[$t]}"
  [ "$(jq -r '.labels["org.opencontainers.image.revision"]' <<<"$rec")" = "$GITHUB_SHA" ] || fail "$t revision label is not $GITHUB_SHA"
  [ "$(jq -r '.labels["org.opencontainers.image.source"]' <<<"$rec")" = "$GITHUB_SERVER_URL/$GITHUB_REPOSITORY" ] || fail "$t source label wrong"
  ok "$t OCI archive: manifest ${MANIFEST[$t]} config ${CONFIG[$t]} linux/amd64 user 65532:65532 entrypoint ${BIN[$t]}"

  # 2. The docker-format archive is the same artifact (same config digest).
  docker load -i "$dock" >/dev/null
  id=$(docker image inspect "$tag" --format '{{.Id}}')
  [ "$id" = "${CONFIG[$t]}" ] || fail "$t loaded image Id $id differs from the OCI config digest ${CONFIG[$t]}"
  [ "$(docker image inspect "$tag" --format '{{.Config.User}}')" = "65532:65532" ] || fail "$t docker config User"
  [ "$(docker image inspect "$tag" --format '{{.Os}}/{{.Architecture}}')" = "linux/amd64" ] || fail "$t docker platform"
  SIZE[$t]=$(docker image inspect "$tag" --format '{{.Size}}')
  ok "$t docker-loaded image Id equals the archive config digest; size ${SIZE[$t]} bytes"

  # 3. Root filesystem contents.
  cid=$(docker create "$tag")
  docker export "$cid" > "$WORK/$t.rootfs.tar"
  tar -tf "$WORK/$t.rootfs.tar" | sed 's#^\./##' > "$WORK/$t.files"
  grep -qx "${BIN[$t]#/}" "$WORK/$t.files" || fail "$t rootfs lacks ${BIN[$t]}"
  ! grep -qx "${OTHER[$t]#/}" "$WORK/$t.files" || fail "$t rootfs contains the other target's binary ${OTHER[$t]}"
  for forbidden in bin/sh bin/bash bin/dash bin/busybox usr/bin/sh usr/bin/bash usr/bin/apt usr/bin/apt-get usr/bin/dpkg sbin/apk usr/bin/apk bin/apk usr/bin/pip3; do
    ! grep -qx "$forbidden" "$WORK/$t.files" || fail "$t rootfs contains $forbidden"
  done
  if grep -E '(^|/)(\.git(/|$)|.*\.pem$|.*\.key$|id_rsa|id_ed25519|\.env$|\.netrc$|\.docker/config\.json$|.*\.db$|go\.sum$|go\.mod$)' "$WORK/$t.files"; then
    fail "$t rootfs contains source-tree or credential-looking files (listed above)"
  fi
  tar -xOf "$WORK/$t.rootfs.tar" etc/passwd | grep -q '^nonroot:x:65532:65532:' || fail "$t /etc/passwd lacks nonroot 65532"
  docker rm "$cid" >/dev/null
  ok "$t rootfs: binary present, other target absent, no shell or package manager, no credential-looking file, nonroot in /etc/passwd ($(wc -l < "$WORK/$t.files") entries)"

  # 4. The binary: static x86-64 ELF from the pinned toolchain.
  tar -xOf "$WORK/$t.rootfs.tar" "${BIN[$t]#/}" > "$WORK/$t.bin"
  python3 - "$WORK/$t.bin" <<'PY'
import struct, sys
data = open(sys.argv[1], "rb").read()
assert data[:4] == b"\x7fELF", "not an ELF file"
assert data[4] == 2, "not ELF64"
e_machine = struct.unpack_from("<H", data, 18)[0]
assert e_machine == 62, f"e_machine {e_machine} is not x86-64"
e_phoff, = struct.unpack_from("<Q", data, 32)
e_phentsize, e_phnum = struct.unpack_from("<HH", data, 54)
types = [struct.unpack_from("<I", data, e_phoff + i * e_phentsize)[0] for i in range(e_phnum)]
assert 3 not in types, "PT_INTERP present: dynamically linked"
print(f"static ELF64 x86-64, {len(data)} bytes, {e_phnum} program headers, no PT_INTERP")
PY
  gov=$(go version "$WORK/$t.bin")
  GOVER[$t]=${gov##*: }
  grep -q "go${EXPECTED_GO_VERSION}$" <<<"$gov" || fail "$t binary built by '$gov', expected go${EXPECTED_GO_VERSION}"
  bi=$(go version -m "$WORK/$t.bin")
  grep -q -- '-trimpath=true' <<<"$bi" || fail "$t binary lacks -trimpath"
  grep -q 'CGO_ENABLED=0' <<<"$bi" || fail "$t binary built with cgo"
  grep -q 'GOOS=linux' <<<"$bi" && grep -q 'GOARCH=amd64' <<<"$bi" || fail "$t binary GOOS/GOARCH"
  ! grep -q 'vcs.revision' <<<"$bi" || fail "$t binary carries a VCS stamp (buildvcs should be false)"
  ok "$t binary: ${GOVER[$t]}, -trimpath, CGO_ENABLED=0, linux/amd64, no VCS stamp"

  # 5. No shell can be executed (behavioural).
  set +e
  docker run --rm --entrypoint /bin/sh "$tag" -c true >"$WORK/$t.sh.out" 2>&1
  rc=$?
  set -e
  [ "$rc" -ne 0 ] || fail "$t: /bin/sh ran successfully; the image has a shell"
  grep -qiE 'no such file|not found|executable file' "$WORK/$t.sh.out" || fail "$t: /bin/sh failed for an unexpected reason: $(cat "$WORK/$t.sh.out")"
  ok "$t: executing /bin/sh fails (exit $rc)"
done

[ "${MANIFEST[gb-server]}" != "${MANIFEST[gbctl]}" ] || fail "server and gbctl images have the same digest"
[ "${CONFIG[gb-server]}" != "${CONFIG[gbctl]}" ] || fail "server and gbctl images have the same config"
ok "server and gbctl images are distinct"

# 6. Runtime: read-only root, non-root, tmpfs data volume, admin through gbctl.
GB_JWT_SECRET=$(openssl rand -base64 48)
GB_ADMIN_TOKEN=$(openssl rand -base64 24)
export GB_JWT_SECRET GB_ADMIN_TOKEN
GBCTL_TOKEN=$GB_ADMIN_TOKEN
export GBCTL_TOKEN
PLAYER=127.0.0.1:18080; ADMIN=127.0.0.1:18081
srv=$(docker run -d --name gb-image-check --read-only --user 65532:65532 --cap-drop ALL --security-opt no-new-privileges \
  --tmpfs /data:rw,uid=65532,gid=65532,mode=0700 \
  -e GB_DB_PATH=/data/gb.db -e GB_ADMIN_ADDR=0.0.0.0:8081 -e GB_JWT_SECRET -e GB_ADMIN_TOKEN -e GB_LOG_LEVEL=info \
  -p "$PLAYER:8080" -p "$ADMIN:8081" gb-server:ci)
cleanup_srv() { docker rm -f "$srv" >/dev/null 2>&1 || true; }
trap 'cleanup_srv; rm -rf "$WORK"' EXIT
for _ in $(seq 1 60); do
  if curl -fsS "http://$PLAYER/readyz" -o "$WORK/readyz.json" 2>/dev/null; then break; fi
  sleep 1
done
[ -s "$WORK/readyz.json" ] || { docker logs "$srv" 2>&1 | tail -50; fail "server did not answer /readyz within 60 s"; }
[ "$(jq -r .mode "$WORK/readyz.json")" = "UNINITIALIZED" ] || fail "readyz mode: $(cat "$WORK/readyz.json")"
[ "$(jq -r .game_ready "$WORK/readyz.json")" = "false" ] || fail "readyz game_ready: $(cat "$WORK/readyz.json")"
curl -fsS "http://$PLAYER/livez" >/dev/null || fail "/livez not 200"
[ "$(curl -s -o /dev/null -w '%{http_code}' "http://$ADMIN/admin/v1/status")" = "401" ] || fail "admin status without a token is not 401"
ok "server started read-only as 65532 on a tmpfs volume: /readyz 200 UNINITIALIZED game_ready=false, /livez 200, admin refuses without token"

# gbctl image against the running server (host network reaches the published ports).
docker run --rm --network host -e GBCTL_SERVER="http://$ADMIN" -e GBCTL_TOKEN gbctl:ci -json status > "$WORK/status.json" || fail "gbctl status failed"
[ "$(jq -r .ok "$WORK/status.json")" = "true" ] && [ "$(jq -r .status.mode "$WORK/status.json")" = "UNINITIALIZED" ] || fail "gbctl status reply: $(cat "$WORK/status.json")"
set +e
docker run --rm --network host -e GBCTL_SERVER="http://$ADMIN" -e GBCTL_TOKEN=wrong-token-0123456789 gbctl:ci status >/dev/null 2>&1
rc=$?
set -e
[ "$rc" -eq 3 ] || fail "gbctl with a wrong token exited $rc, expected 3"
ok "gbctl image administers the server: status UNINITIALIZED, wrong token exit 3"

# Orderly stop.
docker stop -t 60 "$srv" >/dev/null
rc=$(docker wait "$srv")
[ "$rc" = "0" ] || { docker logs "$srv" 2>&1 | tail -50; fail "server exit code on SIGTERM is $rc, expected 0"; }
docker logs "$srv" 2>&1 | grep -q 'shutdown complete' || { docker logs "$srv" 2>&1 | tail -50; fail "no 'shutdown complete' log line"; }
if docker logs "$srv" 2>&1 | grep -qF -- "$GB_ADMIN_TOKEN"; then fail "the admin token appears in the server log"; fi
if docker logs "$srv" 2>&1 | grep -qF -- "$GB_JWT_SECRET"; then fail "the JWT secret appears in the server log"; fi
cleanup_srv
ok "SIGTERM: orderly shutdown, exit 0, secrets absent from the log"

# Missing secrets must be refused.
set +e
docker run --rm --read-only --user 65532:65532 --tmpfs /data:rw,uid=65532,gid=65532 -e GB_DB_PATH=/data/gb.db gb-server:ci >"$WORK/nosecret.out" 2>&1
rc=$?
set -e
[ "$rc" -ne 0 ] || fail "server started without secrets"
ok "server refuses to start without secrets (exit $rc)"

# 7. Record.
jq -n \
  --arg sha "$GITHUB_SHA" --arg run "$GITHUB_SERVER_URL/$GITHUB_REPOSITORY/actions/runs/$GITHUB_RUN_ID/attempts/${GITHUB_RUN_ATTEMPT:-1}" \
  --arg epoch "${SOURCE_DATE_EPOCH:-}" --arg go "$EXPECTED_GO_VERSION" \
  --arg sm "${MANIFEST[gb-server]}" --arg sc "${CONFIG[gb-server]}" --arg ss "${SIZE[gb-server]}" --arg st "${TARSIZE[gb-server]}" --arg sg "${GOVER[gb-server]}" \
  --arg cm "${MANIFEST[gbctl]}" --arg cc "${CONFIG[gbctl]}" --arg cs "${SIZE[gbctl]}" --arg ct "${TARSIZE[gbctl]}" --arg cg "${GOVER[gbctl]}" \
  '{
    kind: "gb-image-build-record", built_by: "ci docker job (no push)", source_commit: $sha, workflow_run: $run,
    source_date_epoch: $epoch, go_toolchain: $go, platform: "linux/amd64",
    dockerfile: "deploy/docker/Dockerfile", context: "go/",
    images: {
      "gb-server": {target: "server", manifest_digest: $sm, config_digest: $sc, image_size_bytes: ($ss|tonumber), oci_archive_bytes: ($st|tonumber), go_version: $sg, intended_repository: "ghcr.io/phreddrick/gb-server"},
      "gbctl":     {target: "gbctl",  manifest_digest: $cm, config_digest: $cc, image_size_bytes: ($cs|tonumber), oci_archive_bytes: ($ct|tonumber), go_version: $cg, intended_repository: "ghcr.io/phreddrick/gbctl"}
    },
    note: "digests are of the local OCI archives; a published digest exists only after publish-images.yml pushes these bytes"
  }' > "$RECORD_OUT"
cat "$RECORD_OUT"
if [ -n "${GITHUB_STEP_SUMMARY:-}" ]; then
  { echo "## Image build record (no push)"; echo '```json'; cat "$RECORD_OUT"; echo '```'; } >> "$GITHUB_STEP_SUMMARY"
fi
echo "image-checks: ALL PASS"
