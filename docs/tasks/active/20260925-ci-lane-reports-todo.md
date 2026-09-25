**Created**: 2026-09-25

# Machine-readable CI lane reports

Give `agent-iterate-ci.yml` a diagnosis that names what failed, instead of the
last 40 KB of a build log.

## Motivation

`agent-iterate-ci.yml`'s "Summarize the failure" step is:

```sh
gh run view "$RUN_ID" --log-failed | tail -c 40000
```

Its own comment records why: the pipeline this was ported from downloads a
machine-readable artifact and renders it, *no lane in this repository writes
one*, so the step was replaced with the log tail.

The tail is a poor diagnosis for the lane that fails most expensively. A
`go test -tags integration -race -v ./...` failure prints tens of megabytes;
40 KB of the end is whichever packages happened to finish last, and the
`--- FAIL:` line, the race report and the panic may all be megabytes earlier.
The fixing agent is then asked to converge in one round from output that need
not contain the failure at all.

## Plan

- [x] Write this todo and the matching lessons file.
- [x] Write `docs/design/ci-lane-reports.md` and add its row to
      `docs/design/README.md`.
- [x] `scripts/ci/lane-failure.mjs` — the per-kind failure summariser. Pure
      functions over captured output; no process spawning, so importing it
      cannot run anything.
- [x] `scripts/ci/run-lanes.mjs` — the lane runner and the lane manifest.
      Runs a named lane, streams its output to the job log, and writes
      `.ci-reports/lane-<name>.json`. `--finish` writes `summary.json` over
      the whole manifest, so a lane that never ran is recorded as `skip` (an
      earlier lane failed) or `filtered` (its precondition did not hold).
- [x] `scripts/ci/summarize-ci.mjs` — renders the reports into the block the
      fixing agent reads, and says nothing at all when no lane failed.
- [x] Suites under `scripts/test/`, planted-tree only for anything that
      executes, plus a small set of guards that read this tree's `ci.yml`.
- [x] `.gitignore`: add `.ci-reports/`, and say why it is not
      `.harness-reports/`.
- [x] Wire the producer: every check step in `ci.yml`'s `build` job becomes a
      lane invocation, plus an `if: always()` finish step and the artifact
      upload (`include-hidden-files: true` — the directory is hidden).
- [x] Wire the consumer: `agent-iterate-ci.yml` downloads the artifact from
      the triggering run and renders it, keeping the log tail as the fallback.
- [x] Update `scripts/README.md`, the stale "`ci.yml` has no upload-artifact
      step" comment in `agent-review-panel.yml`, and the licence-gate guard in
      `harness-hooks.test.mjs` that asserted the old step spelling.

## Verification

- [x] `make verify`
- [x] `node --test 'scripts/test/**/*.test.mjs'`
- [x] `cd scripts/agent && npm ci && npm test`
- [x] `actionlint` over `.github/workflows/`
- [x] End-to-end locally: break a Go test, run the runner, show that the
      summariser names the failing test and that `summarize-ci.mjs` renders
      the block a fixing agent would receive. Recorded in the lessons file.

## Review

The five items in the brief all landed. What the change does **not** do:

- No path filtering. `ci.yml` already filters at the job level with
  `dorny/paths-filter`, and the `build` job's filter is `'**'`, so a second
  script-level filter would be a second source of truth that can disagree with
  the first. The `filtered` state exists anyway, for a different and real
  precondition — see the design doc.
- `make verify` is untouched, and so is what each lane runs. Every lane
  command is the literal string the step it replaced ran, which is what keeps
  `review-panel.mjs`'s `MECHANICAL_COVERAGE_NOTE` and
  `agent-command-verbs.md` §4b true without editing either.
