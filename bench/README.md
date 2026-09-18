# Benchmark baseline

`baseline.json` is the detection measurement this repo gates on. Merging a pack
change auto-cuts a signed release that clients install within a day, so lint
alone is not enough: a well-formed pack can still detect worse than the one it
replaced.

## What it measures

The ruleset a user actually runs — airom's built-in packs with **every pack in
this repo layered over them** — against the labelled corpus in
[airomhq/airom-bench](https://github.com/airomhq/airom-bench), pinned to a
commit. Not the built-ins alone: that is what airom's own CI measures, and it
would not notice a change made here.

Both inputs are pinned in the workflows (`AIROM_VERSION`, `BENCH_CORPUS_SHA`)
because all three of airom, the corpus and this repo move independently. Without
pins, "the numbers changed" would not say which of them changed.

## Regenerating it

Only when the numbers legitimately move — a new pack that detects more, or a
corpus that grew. Regenerate in the same commit that causes the move, so the
diff shows the cause next to the effect:

```bash
pip install airom==0.4.6                     # the pinned AIROM_VERSION
git clone https://github.com/airomhq/airom-bench /tmp/airom-bench
git -C /tmp/airom-bench checkout <BENCH_CORPUS_SHA>

args=(); while IFS= read -r p; do args+=(--rules "$p"); done \
  < <(find rules -name '*.yaml' -not -path '*/testdata/*' | sort)
airom bench /tmp/airom-bench --no-cached-rules "${args[@]}" --json bench/baseline.json
```

Run `.github/scripts/bench_gate.sh /tmp/airom-bench bench/baseline.json` first
to see what moved. A baseline regenerated to make a red gate green, without an
explanation of what improved, is the failure mode this file exists to prevent:
the numbers stop being evidence the moment they are fitted to the result.

## Reading a failure

- **exit 1 — regression.** Detection got worse: precision or recall dropped past
  the threshold, or a trap fired. Fix the pack, or explain the improvement and
  regenerate.
- **exit 2 — the harness broke.** Unreadable corpus, malformed truth file, bad
  flags. Nothing is known about detection quality; do not regenerate the
  baseline to clear it.
