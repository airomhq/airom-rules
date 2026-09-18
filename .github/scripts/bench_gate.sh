#!/usr/bin/env bash
# Measure the ruleset users actually get — airom's built-in packs with every
# pack in this repo layered over them — against the labelled corpus, and fail on
# a detection regression.
#
# Why this exists: merging a pack change here auto-cuts a signed release, and
# clients install it within a day. Without this gate a rules edit could change
# detection for every user without passing the corpus measurement that gates
# airom's own main, which is the one thing that would notice.
#
# Usage: bench_gate.sh <corpus-dir> <baseline.json>
#
# errexit is OFF deliberately. This script classifies bench's exit code, and
# GitHub runs a `run:` step as `bash -e {0}` — a non-zero bench would kill the
# script before any classification ran. airom's fuzz campaign had exactly that
# bug and reported a benign timing race as a crash for three nights.
set +e
set -uo pipefail

corpus=${1:?usage: bench_gate.sh <corpus-dir> <baseline.json>}
baseline=${2:?usage: bench_gate.sh <corpus-dir> <baseline.json>}

# --rules per pack, not a glob: the flag is repeatable and takes one file.
args=()
while IFS= read -r p; do
  args+=(--rules "$p")
done < <(find rules -name '*.yaml' -not -path '*/testdata/*' | sort)

if [ ${#args[@]} -eq 0 ]; then
  echo "::error::no rule packs found under rules/ — the enumeration broke, and an empty overlay would measure the built-ins and call it a pass"
  exit 2
fi
echo "measuring $(( ${#args[@]} / 2 )) pack(s) against $corpus, baseline $baseline"

# --no-cached-rules keeps this hermetic: the overlay is the checkout, never a
# bundle some earlier step happened to fetch.
airom bench "$corpus" --no-cached-rules "${args[@]}" --baseline "$baseline" \
  > bench-report.md 2> bench-gate.txt
code=$?

cat bench-report.md
echo
cat bench-gate.txt
if [ -n "${GITHUB_STEP_SUMMARY:-}" ]; then
  {
    cat bench-report.md
    echo
    echo '### Gate'
    echo '```'
    cat bench-gate.txt
    echo '```'
  } >> "$GITHUB_STEP_SUMMARY"
fi

# Exit codes are airom's CLI contract: 1 is a policy failure (detection got
# worse), 2 means the benchmark could not run at all (unreadable corpus, bad
# truth file, bad flags). Telling them apart matters because the response
# differs — one is a rules bug, the other is a broken harness.
case $code in
  0) echo "::notice::benchmark gate passed against $baseline" ;;
  1) echo "::error::benchmark REGRESSION — this ruleset detects worse than the baseline; see the summary"; exit 1 ;;
  *) echo "::error::benchmark could not run (exit $code) — harness or corpus problem, not a detection result"; exit "$code" ;;
esac
