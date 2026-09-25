---
title: ci-lane-reports
target-version: 0.7.24
---

# CI Lane Reports

## Problem

`agent-iterate-ci.yml` dispatches a fixing agent when CI reds on an
agent-managed branch. Its entire diagnosis was one line:

```sh
gh run view "$RUN_ID" --log-failed | tail -c 40000
```

That step's own comment records why it is there: the pipeline this was ported
from downloads a machine-readable artifact and renders it with
`summarize-ci.mjs`, **no lane in this repository writes one**, so the step
would have downloaded nothing on every run and the fixer would have been
dispatched with an empty prompt. The log tail was the honest fallback.

It is a poor diagnosis for the lane that fails most expensively. `ci.yml`'s
`build` job runs `go test -tags integration -race -coverpkg=./... -v ./...`,
which prints tens of megabytes. The last 40 KB of that is whichever packages
happened to finish last; the `--- FAIL:` line, the race report and any panic
may all be megabytes earlier. The agent is then asked to "converge in ONE
round" from output that need not contain the failure at all. Every wasted
round costs a full CI run — MongoDB, `-race`, the whole suite — and counts
against `MAX_ATTEMPTS`, after which a human is paged.

### Goals

- Every check in `ci.yml`'s `build` job reports itself as a named lane, with a
  machine-readable status.
- A failing lane is summarised in ONE line that names what failed — for Go,
  the failing test, not the first line containing the word "error".
- The fixing agent reads that instead of a log tail, and falls back to the log
  tail whenever the reports cannot name a failure.
- What CI proves does not change.

### Non-Goals

- Replacing `make verify`, or changing any lane's command.
- Splitting the job into more lanes than it already has steps.
- Path-aware lane filtering — see the decision table.
- The review panel's fix arm, which reads CI's conclusion and not its logs.

## Design

Three modules under `scripts/ci/`, one artifact, and two workflow edits.

```
ci.yml (build job)                      agent-iterate-ci.yml (iterate job)
─────────────────────                   ─────────────────────────────────
run-lanes.mjs lint         ┐
run-lanes.mjs license      │ writes     download-artifact (run-id = the
run-lanes.mjs proto-lint   │ .ci-reports/  run that triggered us)
…                          │   lane-<name>.json        │
run-lanes.mjs test         ┘   summary.json            ▼
run-lanes.mjs --finish     ── always() ──►  summarize-ci.mjs --dir …
upload-artifact ci-lane-reports              │
                                             ├─ a block → the fixer's prompt
                                             └─ empty   → gh run view --log-failed
```

### The lane manifest

`scripts/ci/run-lanes.mjs` exports `LANES`, one entry per check the `build`
job already ran, in the same order:

| lane | kind | command |
|---|---|---|
| `lint` | `golangci` | `make lint` |
| `license` | `license` | `node scripts/verify-license.mjs` |
| `proto-lint` | `buf` | `buf lint` |
| `proto-breaking` | `buf` | `buf breaking --against …#ref=$LANE_BASE_SHA` |
| `codegen-fresh` | `generic` | `buf generate` + a `git status` assertion on `api/` |
| `build` | `go-build` | `make build` |
| `vet-tagged` | `go-build` | `go vet -tags rgafuzz ./...` |
| `test` | `go-test` | `go test -tags integration -race … -v ./...` |

Each `run` is the **literal string** the step it replaced carried, executed
with `bash -e -c` because that is what a `run:` step is — `codegen-fresh` is
multi-command, and `-e` is what GitHub's default shell applies. Dropping the
flag is not a style choice: a multi-command lane would then report its LAST
command's status, so `codegen-fresh` could ignore `buf generate` failing,
find `api/` clean because nothing regenerated, and report **pass** — removing
a CI gate on a green run. `-o pipefail` is deliberately absent, since GitHub
applies it only to a step that sets `shell: bash` and none here does; adding
it would make a lane stricter than the step it replaced. This is
load-bearing: `review-panel.mjs`'s
`MECHANICAL_COVERAGE_NOTE` and [agent-command-verbs.md](agent-command-verbs.md)
§4b are an inventory of what CI proves, and both stay true only while this
file changes how a lane is reported and never what it runs.

`ci.yml` keeps one step per lane — `node scripts/ci/run-lanes.mjs <name>` —
rather than one step running the manifest. The reports are for the fixing
agent; the Actions UI is for people, and collapsing eight named steps into
"Run the CI lanes" would take the step name that says which check failed away
from a human to give a machine something it already had. Run with no
arguments the CLI does execute the whole manifest in order, stopping at the
first failure, which is what makes it useful locally.

### Four states, and why `skip` is not `filtered`

| state | meaning |
|---|---|
| `pass` | the lane ran and exited 0 |
| `fail` | the lane ran and did not |
| `skip` | the lane never ran, because an earlier one failed |
| `filtered` | the lane never ran, because a precondition did not hold |

The last two are different facts about the branch. A `skip` will run on the
next push; a `filtered` lane cannot run on this kind of event at all.
Collapsing them makes the summary state something untrue — that a lane was cut
short by an earlier failure when it was never applicable.

Today there is exactly one precondition: `proto-breaking` needs the pull
request's base commit, which exists on a `pull_request` event and not on a
push to `main`. It is declared on the lane (`precondition: { env:
'LANE_BASE_SHA', why: … }`) rather than as a step-level `if:`, because a
step-level condition is invisible to the finish step — which has to evaluate
the same predicate over the whole manifest, from an environment in which the
lane never ran.

**This is precondition filtering, not path filtering.** No lane is chosen or
skipped by which files changed. `ci.yml` already does that at the job level
with `dorny/paths-filter`, and a second script-level filter would be a second
source of truth that can disagree with the first — while the `build` job's
own filter is `'**'`, so there is nothing for it to decide.

### Bounding an arbitrarily large stream

A report is a fixed size no matter how loud the lane was, and it gets there by
keeping two views that fail in different ways:

- **The tail** — the last 16 KB. Position-dependent; catches the ordinary
  context around a failure that matched no pattern.
- **The notable lines** — up to 60 lines matching the lane kind's patterns,
  retained **as they stream past**. Position-independent; this is what catches
  a `WARNING: DATA RACE` that fired four megabytes before the end.

A larger tail is not a substitute for the second. It is the same design, more
expensive, and it still loses the early race report — which is the exact case
this subsystem exists for.

**The patterns must not match a passing run's output.** The first version
matched `ok  <pkg>` and every `<file>_test.go:NN:` line; against a real
`go test -race -v ./...` those are most of the 666 KB, the 60-line budget
filled with passing packages long before the stream reached the failing one,
and the summary came out as the word `FAIL`. A pattern that matches what a
green run prints does not select evidence, it evicts it.

That leaves the one line worth having — `    tree_test.go:214: expected …` —
unmatched, because it is identical whether the test it belongs to passed or
failed. It is kept by **lookbehind** instead: the capture holds the last eight
context-shaped lines and flushes them only when a failure-shaped line arrives,
which is exactly where `go test` prints them.

A third category is needed for that to hold. `go test` prints
`=== RUN Parent/next_case` between the assertion and the `--- FAIL:` whenever
the failing subtest is not the last under its parent — the commonest shape —
and treating that as "something else" clears the window and discards the line.
The `===` announcements are therefore **neutral**: they leave the lookbehind
alone. `--- PASS` and `--- SKIP` are deliberately not, because they do mean
the output above them belonged to a test that did not fail.

The full output is still streamed unchanged to the job log. Nothing is hidden;
what is bounded is the report.

### Naming the failure

`scripts/ci/lane-failure.mjs` is a separate module with no I/O of any kind, so
the suite can test the parsing exhaustively without a Go toolchain and
importing it cannot run a build. Per kind, with an explicit precedence order,
because one run can carry several failure shapes and only one is the cause:

For `go-test`: a package that did not **compile** cannot have a failing test,
so the compile error wins; a **data race** is what the `-race` lane exists to
find, so it wins over the `--- FAIL:` it causes; a **panic** aborts the binary,
so the tests after it are collateral; otherwise the first `--- FAIL:` —
which, since `go test -v` prints the deepest subtest first, is the most
specific name and the one that can be handed to `go test -run`.

A failing lane always produces a non-empty line. `exited 2 with no output` is
true and useful; a blank cell reads as "nothing failed".

### The consumer, and the fallback

`summarize-ci.mjs` renders the block. Its contract is **say nothing or say
something useful**: it writes nothing to stdout and exits 3 whenever no lane
failed — a missing artifact, a truncated one, or a red run whose failure was
outside the lanes entirely (a `make tools` that died, a runner out of disk).

`agent-iterate-ci.yml` keys its fallback off exactly that: an empty capture
means `gh run view --log-failed` is still the better diagnosis. The order is
strict, and the step can never leave the prompt empty — that is the one
outcome worse than a coarse diagnosis, and it is why the log tail stays.

The renderer runs from `$RUNNER_TEMP/trusted-scripts`, staged from `main`,
never from the workspace: the checkout there is the pull request's branch, and
a branch does not get to supply the script that renders what the job puts in
front of an agent.

### Risks and Mitigation

| Risk | Mitigation |
|------|------------|
| A lane is added to the manifest and no `ci.yml` step invokes it — a gate that exists and runs nowhere | `run-lanes.test.mjs` asserts set equality between `LANES` and the invocations in `ci.yml`; the CLI exits 2 on an unknown lane name, so a typo in the workflow is a red step |
| The artifact uploads nothing, silently | `include-hidden-files: true` (the search root `.ci-reports` is a dot-directory), plus the existing guard in `review-panel.test.mjs` that fails any upload step in this directory globbing a hidden path without it |
| The reports name a failure the run did not have, or miss one it did | The summary is written from the manifest, never from the files present, so a lane that never ran is stated as `skip`/`filtered` rather than omitted |
| A fixer is dispatched with an empty diagnosis | The renderer prints nothing rather than a table of skips, and the consumer falls back to the log tail; both halves are tested |
| Node becomes load-bearing for every lane in the `build` job | It already was for the licence gate; the job now pins Node 22 with `setup-node`, the same version `docs.yml` and `agent-scripts.yml` use |
| Test output closes the rendered code fence early and spills into the prompt as instructions | The fence is widened past any run of backticks in the body; asserted in `summarize-ci.test.mjs` |

### Design Decisions

| Decision | Reason |
|----------|--------|
| `.ci-reports/`, not `.harness-reports/` | The upstream name was already taken here by a gitignored scratch directory `novelty.test.mjs` plants **git repositories** under. Sharing it would put a nested `.git` inside the upload's search root and race two writers for one path; renaming the scratch directory instead would edit a test's isolation guarantee to make room for a reports sink |
| One step per lane in `ci.yml` | Keeps the step name that tells a human which check failed. The reports serve the agent; the UI serves people |
| Lane commands are the literal former `run:` strings | `MECHANICAL_COVERAGE_NOTE` and §4b stay true with no edit. A rewritten command would be a behaviour change smuggled in beside a reporting change |
| Stop at the first failing lane | What `ci.yml` does with its steps today. Running on would change what a red branch costs, which is a decision about the pipeline, not one this file gets to make on the way past |
| A separate module for the failure parsing | It is the only part that can be tested exhaustively and cheaply, and the runner must be importable without running a suite |
| The renderer says nothing rather than something empty | A half-wired chain that hands an agent a table of eight skipped lanes and no cause is worse than no chain at all |
| `retention-days: 7` | A hand-off to a loop that runs minutes later. A week covers a human reading one while debugging a fixer, on the busiest workflow in the repository |

## Alternatives Considered

| Alternative | Why not |
|-------------|---------|
| Keep the log tail, just make it bigger | Same design, more expensive, and it still misses a race report that fired early in a 40 MB run |
| One `ci.yml` step running the whole manifest | Collapses eight named steps into one in the Actions UI, and forces the two conditional lanes' `if:` into JavaScript |
| A separate job that re-runs the checks to produce reports | Doubles the cost of the most expensive job in the repository to report on it |
| Reuse `.harness-reports/` | See the decision table — the name is taken by something with incompatible contents |
| Path-aware lane filtering | `ci.yml` already filters at the job level; a second filter is a second source of truth that can disagree |
| Parse the log tail in the consumer instead | The producer is the only place with the whole stream. By the time the log is fetched, the failure may already be outside the 40 KB |

## Tasks

Track execution plans in `docs/tasks/active/` as separate task documents —
`20260925-ci-lane-reports-todo.md` for this one.
