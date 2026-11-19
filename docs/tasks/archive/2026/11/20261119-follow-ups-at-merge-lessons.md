# File out-of-diff follow-ups once per merged PR — Lessons

**Created**: 2026-11-19

## What the old filing cost

- Read the closing comment of every `not planned` follow-up before choosing a
  fix. The 26 split into four causes, and each cause needed a different remedy:
  the cap per run did nothing for duplicates, and the dedup did nothing for
  code that never merged.
- The per-run cap of three was per round, not per PR. #2196 filed 10 issues
  across its rounds. Its last round's deferred record holds one finding, which
  #2203 already tracks, so the new flow files nothing for it.

## Checks run against real data

- `collect` (read-only) on merged #2397 and #2196: the newest deferred record
  is found, and every finding in it matches a follow-up already filed, so
  nothing reaches the judge.
- The judge, run locally on four findings rebuilt from filed follow-ups with
  the filed copies hidden from its candidate list: it matched #2262 → #2253
  (a human issue), #2215 → #2214, #2182 → #2181 (a human issue), and left
  #2213 to be filed. That is the outcome the maintainers reached by hand. One
  session, 35 seconds, 156 candidate issues.
- The mechanical fold is a similarity call and can over-merge: #2397's four
  deferred findings fold into one item, though they were filed as two issues
  (#2346, #2407). So a folded item shows each extra finding's own summary,
  and a reader can see the merge.

## Self review

One round, an independent reviewer given only the diff. No blocking
findings; eight minor, seven fixed:

- A judge killed by the job timeout left no plan, and the file step skipped,
  so nothing was filed. Now the judge step has its own timeout, and `file`
  runs after it and falls back to the collected findings.
- The body had no size bound; GitHub refuses a body over 65536 characters, which
  would have lost every item. Details are now dropped from the tail to fit.
- Folded findings had no hidden record, so a later PR raising one again would
  not match this issue. Each now carries its own.
- PRs merged into a non-default branch were filed too. Now skipped.
- Every verb exited 0, so a lost filing looked green. Failures now exit 1.
- A run that files nothing left no trace; it now writes the matches to the run
  summary.
- A record from an older commit than the merged head is now called out in the
  issue (the reviewer's case: a cancelled last round).

Kept as is: the app-slug trust on the deferred check, which is the merge
gate's own posture (`CHECK_PRODUCER_APP_SLUG`), now imported from there.

## Harness notes

- `config-wiring.test.mjs` counts every place a harness script runs. A new
  workflow and a removed panel step change `invocations`, `fromWorkspace`,
  `fromSideDirectory` and `configReadingInvocations`; check each delta against
  the sites you added or removed before updating `EXPECTED`.
- Regenerate `scripts/agent/MANIFEST.sha256` with `node manifest.mjs build`
  after every edit under `scripts/agent` or to an `agent-*.yml`, or `npm test`
  fails on the manifest check.
