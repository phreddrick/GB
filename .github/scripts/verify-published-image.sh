#!/usr/bin/env bash
# Verify that a digest proposed for the dev entry point is a published, single
# linux/amd64 manifest of ghcr.io/phreddrick/gb-server built from the given
# master commit (Phase 2C brief section 10, requirements 1 to 3).
#
# Environment: IMAGE (ghcr.io/phreddrick/gb-server), DIGEST, SOURCE_SHA,
# REPO_URL, and `crane` on PATH (logged in with a read token if the package
# is private). Checks, in order: digest and commit formats; the commit is on
# origin/master; the manifest is retrievable by digest and is an image
# manifest (not an index); the config's os/architecture, User and
# org.opencontainers.image.revision / .source labels match. Nothing is written.
set -euo pipefail

: "${IMAGE:?}" "${DIGEST:?}" "${SOURCE_SHA:?}" "${REPO_URL:?}"
command -v crane >/dev/null || { echo "crane is required" >&2; exit 2; }
fail() { echo "FAIL $*" >&2; exit 1; }
ok() { echo "PASS $*"; }

[[ "$DIGEST" =~ ^sha256:[0-9a-f]{64}$ ]] || fail "digest '$DIGEST' is not sha256:<64 lower-case hex>"
[ "$DIGEST" != "sha256:0000000000000000000000000000000000000000000000000000000000000000" ] || fail "the all-zero sentinel is not a published digest"
[[ "$SOURCE_SHA" =~ ^[0-9a-f]{40}$ ]] || fail "source commit '$SOURCE_SHA' is not a 40-hex SHA"
[ "$IMAGE" = "ghcr.io/phreddrick/gb-server" ] || fail "image '$IMAGE' is not the server image this entry point pins"
ok "formats: $IMAGE@$DIGEST from $SOURCE_SHA"

git fetch --quiet origin master
git merge-base --is-ancestor "$SOURCE_SHA" origin/master || fail "$SOURCE_SHA is not on origin/master"
ok "source commit is on origin/master"

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
crane manifest "$IMAGE@$DIGEST" > "$tmp/manifest.json" || fail "manifest $IMAGE@$DIGEST is not retrievable (not published, or the package is private and the token cannot read it)"
mt=$(jq -r '.mediaType // empty' "$tmp/manifest.json")
[ "$mt" = "application/vnd.oci.image.manifest.v1+json" ] || fail "manifest media type is '$mt'; expected a single OCI image manifest (an index digest would not be the published form of this build)"
remote=$(crane digest "$IMAGE@$DIGEST")
[ "$remote" = "$DIGEST" ] || fail "registry reports digest $remote for $DIGEST"
crane config "$IMAGE@$DIGEST" > "$tmp/config.json"
[ "$(jq -r '.os + "/" + .architecture' "$tmp/config.json")" = "linux/amd64" ] || fail "platform is $(jq -r '.os + "/" + .architecture' "$tmp/config.json")"
[ "$(jq -r '.config.User' "$tmp/config.json")" = "65532:65532" ] || fail "config User is $(jq -r '.config.User' "$tmp/config.json")"
rev=$(jq -r '.config.Labels["org.opencontainers.image.revision"] // empty' "$tmp/config.json")
[ "$rev" = "$SOURCE_SHA" ] || fail "revision label '$rev' does not equal the source commit $SOURCE_SHA: this digest was not built from that commit"
src=$(jq -r '.config.Labels["org.opencontainers.image.source"] // empty' "$tmp/config.json")
[ "$src" = "$REPO_URL" ] || fail "source label '$src' is not $REPO_URL"
ok "published manifest verified: single linux/amd64 image manifest, user 65532:65532, revision $SOURCE_SHA, source $REPO_URL"
