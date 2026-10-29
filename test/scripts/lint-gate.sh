#!/usr/bin/env bash
# Lint gate used by CI (.github/workflows/ci.yml) and locally.
#
#   test/scripts/lint-gate.sh [report.json]
#
# Runs golangci-lint (pinned) from go/, distinguishes "findings reported"
# (exit 1, a normal result) from tool failures (any other nonzero exit, which is
# fatal), validates the JSON report, and compares it with the committed
# baseline using tools/lintdiff. Exit status: 0 gate passed, 1 new or stale
# findings, 2 the analysis could not be trusted. The caller must preserve the
# script's exit status (CI uses bash with pipefail when it tees the output).
set -euo pipefail

GOLANGCI_VERSION="${GOLANGCI_VERSION:-v2.15.0}"
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
REPORT="${1:-${RUNNER_TEMP:-/tmp}/lint.json}"
BASELINE="$ROOT/docs/testing/lint-baseline.txt"

cd "$ROOT/go"
rm -f "$REPORT"
# Build the tools into a private directory and run the binaries directly:
# `go run` collapses every nonzero program exit into status 1, which would hide
# the difference between "findings" (1) and tool failures (3..7), and between
# lintdiff's 1 (findings) and 2 (untrusted input).
BIN="$(mktemp -d)"
trap 'rm -rf "$BIN"' EXIT
GOBIN="$BIN" go install "github.com/golangci/golangci-lint/v2/cmd/golangci-lint@${GOLANGCI_VERSION}"
go build -o "$BIN/lintdiff" ./tools/lintdiff
set +e
"$BIN/golangci-lint" run --output.json.path="$REPORT" ./... >/dev/null
lint_status=$?
set -e
case "$lint_status" in
  0) echo "lint-gate: golangci-lint ran clean (exit 0)" ;;
  1) echo "lint-gate: golangci-lint reported findings (exit 1); comparing with the baseline" ;;
  *) echo "lint-gate: FATAL: golangci-lint exit status $lint_status is a tool failure (3 failure, 4 timeout, 5 no files, 6 no config, 7 error logged), not a findings result" >&2
     exit 2 ;;
esac
if [ ! -s "$REPORT" ]; then
  echo "lint-gate: FATAL: no report written to $REPORT" >&2
  exit 2
fi
# lintdiff validates the report (schema, Report.Error, enabled linters) and compares.
"$BIN/lintdiff" -baseline "$BASELINE" -report "$REPORT" -prefix go/
