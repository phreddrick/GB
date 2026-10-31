#!/usr/bin/env bash
# Preflight for publish-images.yml (Phase 2C brief sections 5 and 9; GB2C-02).
# Refuses to let the privileged publish job run unless:
#   1. the typed confirmation CONFIRM is exactly `publish`;
#   2. the dispatched ref is refs/heads/master and the commit is on origin/master;
#   3. ONE workflow run of the approved ci workflow vouches for this exact
#      commit: resolved through the workflow-runs API (repository, workflow
#      path, head_sha, branch master, event push, completed, success, exactly
#      one eligible run), and every job named in REQUIRED_CHECKS belongs to
#      that run and that attempt and completed with success. Check runs are
#      not consulted: their names can be produced by any workflow.
# The selection policy is documented in ci_provenance.py.
# Environment: CONFIRM, GITHUB_REF, GITHUB_SHA, GITHUB_REPOSITORY, GH_TOKEN
# (actions: read), REQUIRED_CHECKS (one job name per line), optional
# CI_WORKFLOW_PATH (.github/workflows/ci.yml), CI_BRANCH (master), CI_EVENT
# (push). Nothing is written anywhere but a temporary directory.
set -euo pipefail

: "${GITHUB_REF:?}" "${GITHUB_SHA:?}" "${GITHUB_REPOSITORY:?}" "${GH_TOKEN:?}" "${REQUIRED_CHECKS:?}"
CI_WORKFLOW_PATH=${CI_WORKFLOW_PATH:-.github/workflows/ci.yml}
CI_BRANCH=${CI_BRANCH:-master}
CI_EVENT=${CI_EVENT:-push}
HERE=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
fail() { echo "FAIL $*" >&2; exit 1; }
ok() { echo "PASS $*"; }
for tool in gh git jq python3; do
  command -v "$tool" >/dev/null || fail "$tool is required"
done
[[ "$GITHUB_SHA" =~ ^[0-9a-f]{40}$ ]] || fail "GITHUB_SHA is not a 40-hex commit"

[ "${CONFIRM:-}" = "publish" ] || fail "confirmation input is not the literal word publish"
ok "confirmation typed"

[ "$GITHUB_REF" = "refs/heads/$CI_BRANCH" ] || fail "dispatched on $GITHUB_REF; publication is allowed from refs/heads/$CI_BRANCH only"
git fetch --quiet origin "$CI_BRANCH"
git merge-base --is-ancestor "$GITHUB_SHA" "origin/$CI_BRANCH" || fail "$GITHUB_SHA is not on origin/$CI_BRANCH"
ok "ref $CI_BRANCH, commit $GITHUB_SHA is on origin/$CI_BRANCH (origin/$CI_BRANCH is $(git rev-parse "origin/$CI_BRANCH"))"

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
api=(gh api -H "Accept: application/vnd.github+json" -H "X-GitHub-Api-Version: 2022-11-28")
workflow_file=$(basename "$CI_WORKFLOW_PATH")

echo "workflow runs of $CI_WORKFLOW_PATH for $GITHUB_SHA:"
"${api[@]}" "repos/$GITHUB_REPOSITORY/actions/workflows/$workflow_file/runs?head_sha=$GITHUB_SHA&per_page=100" > "$tmp/runs.json" \
  || fail "the workflow-runs query failed; no evidence, no publication"
python3 "$HERE/ci_provenance.py" select-run --runs "$tmp/runs.json" --repository "$GITHUB_REPOSITORY" \
  --workflow-path "$CI_WORKFLOW_PATH" --sha "$GITHUB_SHA" --branch "$CI_BRANCH" --event "$CI_EVENT" --out "$tmp/selected.json" \
  || fail "no single trustworthy ci run vouches for $GITHUB_SHA; publication refused"
run_id=$(jq -r .run_id "$tmp/selected.json")
run_attempt=$(jq -r .run_attempt "$tmp/selected.json")
[[ "$run_id" =~ ^[0-9]+$ && "$run_attempt" =~ ^[0-9]+$ ]] || fail "selected run identity is malformed"

echo "jobs of run $run_id attempt $run_attempt:"
"${api[@]}" "repos/$GITHUB_REPOSITORY/actions/runs/$run_id/attempts/$run_attempt/jobs?per_page=100" > "$tmp/jobs.json" \
  || fail "the jobs query for run $run_id attempt $run_attempt failed"
printf '%s\n' "$REQUIRED_CHECKS" > "$tmp/required.txt"
python3 "$HERE/ci_provenance.py" verify-jobs --jobs "$tmp/jobs.json" --run-id "$run_id" --run-attempt "$run_attempt" \
  --sha "$GITHUB_SHA" --required-checks "$tmp/required.txt" \
  || fail "not every required job of run $run_id attempt $run_attempt succeeded for $GITHUB_SHA; publication refused"

if [ -n "${GITHUB_STEP_SUMMARY:-}" ]; then
  { echo "## Preflight"; echo "ci run $(jq -r .html_url "$tmp/selected.json") (attempt $run_attempt) vouches for \`$GITHUB_SHA\`."; } >> "$GITHUB_STEP_SUMMARY"
fi
ok "preflight complete: ci run $run_id attempt $run_attempt vouches for $GITHUB_SHA; publication may proceed"
