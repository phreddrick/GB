#!/usr/bin/env bash
# Propose a verified server digest in the dev entry point as a pull request
# (Phase 2C brief section 10, requirements 4 to 12). Runs after
# verify-published-image.sh in digest-update-pr.yml, on a checkout of master
# whose token has contents: write and pull-requests: write.
#
# Environment: DIGEST, SOURCE_SHA, IMAGE, ENTRY_POINT, GH_TOKEN, RUN_URL,
# APPROVED_RUN_SH_SHA256, KUSTOMIZE_RENDER_DIR, GITHUB_REPOSITORY.
#
# Steps: refuse if master already pins the digest, if the branch exists on
# origin, or if an open pull request already proposes it (no duplicates;
# exit 0 with a notice); rewrite exactly one line with set_image_digest.py;
# require the diff to be that one line; run the unchanged offline validation
# runner in strict mode (`--require-published-image` must now pass); commit
# as github-actions[bot]; push the branch; open the pull request with gh. If
# gh cannot create it (repository setting "Allow GitHub Actions to create
# and approve pull requests" off, or a ruleset), the branch stays pushed, the
# compare URL is printed for a manual pull request, and the job fails so the
# missing owner action is visible. Never pushes to master, never merges.
#
# What happens after creation (GB2C-05; GitHub docs, "GITHUB_TOKEN", section
# "When GITHUB_TOKEN triggers workflow runs", https://docs.github.com/en/
# actions/concepts/security/github_token): the opened/synchronize/reopened
# events of a pull request created with GITHUB_TOKEN create workflow runs in
# an approval-required state; a user with write access starts them with
# "Approve workflows to run" in the pull request's merge box. Labels do not
# approve anything (a `labeled` event from the token creates no run; a label
# applied by a maintainer re-triggers ci only because ci.yml lists `labeled`,
# and it is not needed here: the digest file is not protected tooling). The
# sequence is therefore: (1) setting allows Actions to create pull requests,
# (2) this job pushes and opens it, (3) maintainer approves the workflow run,
# (4) required checks execute, (5) review and merge by a human, (6) Argo CD
# sync as a separate manual step. Manual fallback: open the pull request by
# hand from the pushed branch; it then runs ci like any other pull request.
set -euo pipefail

: "${DIGEST:?}" "${SOURCE_SHA:?}" "${IMAGE:?}" "${ENTRY_POINT:?}" "${GH_TOKEN:?}" "${RUN_URL:?}" "${APPROVED_RUN_SH_SHA256:?}" "${GITHUB_REPOSITORY:?}"
HERE=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
fail() { echo "FAIL $*" >&2; exit 1; }
ok() { echo "PASS $*"; }
note() { echo "NOTE $*"; }

short=${DIGEST#sha256:}; short=${short:0:12}
branch="bot/gb-server-digest-$short"
title="deploy(dev): pin gb-server to sha256:$short (built from ${SOURCE_SHA:0:12})"

[ "$(git rev-parse --abbrev-ref HEAD)" = "master" ] || fail "not on master"
git fetch --quiet origin master
[ "$(git rev-parse HEAD)" = "$(git rev-parse origin/master)" ] || fail "local master is not origin/master"

current=$(python3 "$HERE/set_image_digest.py" --check --file "$ENTRY_POINT" --image "$IMAGE")
if [ "$current" = "$DIGEST" ]; then
  note "master already pins $DIGEST; nothing to propose"
  exit 0
fi
if git ls-remote --exit-code --heads origin "$branch" >/dev/null 2>&1; then
  note "branch $branch already exists on origin; not creating a duplicate"
  gh pr list --repo "$GITHUB_REPOSITORY" --head "$branch" --state open --json number,url --jq '.[] | "open pull request #\(.number) \(.url)"' || true
  exit 0
fi
existing=$(gh pr list --repo "$GITHUB_REPOSITORY" --state open --search "\"sha256:$short\" in:title" --json number,url,headRefName --jq '.[] | select(.headRefName | startswith("bot/gb-server-digest-")) | "#\(.number) \(.url)"' || true)
if [ -n "$existing" ]; then
  note "an open pull request already proposes sha256:$short: $existing"
  exit 0
fi
ok "no existing branch or pull request for $DIGEST"

git checkout -q -b "$branch"
set +e
python3 "$HERE/set_image_digest.py" --file "$ENTRY_POINT" --image "$IMAGE" --digest "$DIGEST"
rc=$?
set -e
[ "$rc" -eq 0 ] || fail "set_image_digest.py exited $rc"
numstat=$(git diff --numstat)
[ "$numstat" = "$(printf '1\t1\t%s' "$ENTRY_POINT")" ] || { git diff; fail "the change is not exactly one line in $ENTRY_POINT: $numstat"; }
git diff
ok "exactly one line changed in $ENTRY_POINT ($current -> $DIGEST)"

actual=$(sha256sum deploy/kustomize/tests/run.sh | cut -d' ' -f1)
[ "$actual" = "$APPROVED_RUN_SH_SHA256" ] || fail "deploy/kustomize/tests/run.sh is not the reviewed runner ($actual)"
mkdir -p "${KUSTOMIZE_RENDER_DIR:-/tmp/render}"
deploy/kustomize/tests/run.sh --require-published-image
ok "offline validation passed in strict mode on the candidate (sentinel gone, identity and structure unchanged)"

git config user.name "github-actions[bot]"
git config user.email "41898282+github-actions[bot]@users.noreply.github.com"
git add "$ENTRY_POINT"
git commit -q -F - <<EOF
$title

Replace the image digest in deploy/kustomize/apps/dev/server/kustomization.yaml
with the manifest digest published by publish-images.yml from commit
$SOURCE_SHA:

    $IMAGE@$DIGEST

Verified before this commit: the manifest is retrievable by digest, is a
single linux/amd64 OCI image manifest, runs as 65532:65532 and carries the
revision label of that commit; the offline validation passed with
--require-published-image. Only the digest line changed. Nothing is
deployed by merging this: Argo CD sync is manual (docs/operations/deploy.md).

Workflow run: $RUN_URL
EOF
git push -q origin "HEAD:refs/heads/$branch"
ok "branch $branch pushed"

body=$(cat <<EOF
Automated digest proposal (Phase 2C, \`digest-update-pr.yml\`, run $RUN_URL).

| Field | Value |
|---|---|
| Image | \`$IMAGE\` |
| Published manifest digest | \`$DIGEST\` |
| Built from commit | \`$SOURCE_SHA\` |
| Previous value | \`$current\` |
| Change | one line in \`$ENTRY_POINT\` |

Verified by the workflow: manifest retrievable by digest; single \`linux/amd64\` OCI image manifest; \`User 65532:65532\`; \`org.opencontainers.image.revision\` equals the commit above; \`deploy/kustomize/tests/run.sh --require-published-image\` passes on this branch.

**This pull request deploys nothing.** Merging it changes the Git desired state only; the \`gb-dev-server\` Argo CD Application is synced manually and separately (\`docs/operations/deploy.md\`).

**Checks need a maintainer's approval first.** This pull request was opened with the workflow \`GITHUB_TOKEN\`, so its \`ci\` run starts in an approval-required state: select **Approve workflows to run** in the merge box ([GitHub docs: GITHUB_TOKEN](https://docs.github.com/en/actions/concepts/security/github_token)). Adding a label does not approve it. Required checks run after approval; no acknowledgment label is needed (the digest file is not protected tooling); merge is a separate human decision. The sentinel comment above the digest line is left for a human to update.
EOF
)
set +e
pr_url=$(gh pr create --repo "$GITHUB_REPOSITORY" --base master --head "$branch" --title "$title" --body "$body" 2>"$RUNNER_TEMP/gh-pr.err")
rc=$?
set -e
if [ "$rc" -ne 0 ]; then
  cat "$RUNNER_TEMP/gh-pr.err" >&2
  compare="https://github.com/$GITHUB_REPOSITORY/compare/master...$branch?expand=1"
  echo "FAIL gh could not open the pull request. The branch $branch is pushed; open it by hand: $compare" >&2
  echo "Owner action if the error says Actions may not create pull requests: Settings > Actions > General > Workflow permissions > enable 'Allow GitHub Actions to create and approve pull requests' (or keep it off and open these pull requests manually)." >&2
  if [ -n "${GITHUB_STEP_SUMMARY:-}" ]; then { echo "## Digest pull request NOT created"; echo "Branch pushed: \`$branch\`. Open manually: $compare"; } >> "$GITHUB_STEP_SUMMARY"; fi
  exit 1
fi
ok "pull request opened: $pr_url (not merged; its ci run awaits 'Approve workflows to run' by a maintainer; required checks run after that; merge is a separate decision; nothing is deployed)"
if [ -n "${GITHUB_STEP_SUMMARY:-}" ]; then { echo "## Digest pull request"; echo "$pr_url"; echo; echo "Not merged. Deploys nothing. Next: a maintainer selects **Approve workflows to run** on the pull request (token-created pull requests start their workflow runs approval-required); required checks execute; review and merge by hand; Argo CD sync is a separate manual step."; } >> "$GITHUB_STEP_SUMMARY"; fi
if [ -n "${GITHUB_OUTPUT:-}" ]; then echo "pr_url=$pr_url" >> "$GITHUB_OUTPUT"; fi
