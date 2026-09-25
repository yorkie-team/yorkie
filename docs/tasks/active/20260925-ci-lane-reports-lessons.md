**Created**: 2026-09-25

# Machine-readable CI lane reports — lessons

## The name was taken, and taking it back would have been the wrong fix

`.harness-reports/` is what the upstream pipeline calls this artifact, and it
already exists here as something else: `scripts/agent/novelty.test.mjs` creates
throwaway **git repositories** under it, and `.gitignore`'s comment says it
picks the location because being ignored is what stops an interrupted run from
leaving a nested `.git` that `git add -A` commits as a gitlink.

Three options, and the one that looks cheapest is the one that breaks:

| Option | Why not |
|---|---|
| Reuse the directory | `upload-artifact` resolves `path:` with `@actions/glob`, whose search root is the directory. A developer who ran `node --test` locally and interrupted it leaves a whole git repository inside that root, and the upload carries it. On a runner the two writers also race for the same path |
| Rename the scratch directory | Edits a test's isolation guarantee to make room for a reports sink. The wrong file to touch, and it silently invalidates a comment that exists because the failure mode already happened once |
| A second directory | One line of `.gitignore`, and the two things that are not the same thing stop sharing a name |

`.ci-reports/` it is. The cost is that the name no longer matches upstream's,
which matters only to a future sync — and a sync that renames a directory is a
smaller problem than one that has to un-merge two unrelated uses of it.

## `filtered` earned its place without path filtering

The brief said to adopt upstream's fourth state only alongside path filtering.
Path filtering was not implemented — `ci.yml` already filters at the job level
with `dorny/paths-filter`, and a second script-level filter would be a second
source of truth that can disagree with the first.

But the principle behind `filtered` is not about paths. It is that "did not
run because something earlier failed" and "cannot run on this event" are
different facts, and a summary that says `skip` for both tells a fixing agent
that a lane was cut short when in fact it was never applicable. This repository
has exactly one instance of the second: `buf breaking` needs the pull request's
base commit, which does not exist on a push to `main`. So the state was adopted
for that, and the design doc says plainly that it is precondition filtering and
not path filtering, so the next reader does not infer a capability that is not
there.

The side effect is the better half: moving the condition out of the step's
`if:` and into the lane's `precondition` made it readable by the finish step.
A step-level `if:` is invisible to anything that runs afterwards, which is why
the summary could not have distinguished the two states while it lived there.

## Two bounded views beat one bigger one

The first instinct was "keep a larger tail" — 40 KB was too small, so keep
256 KB. That is the same design, more expensive. It still loses a race report
that fired four megabytes before the end of a `-race` run, which is precisely
the failure this exists for.

What works is keeping two things that fail differently: the last 16 KB
(position-dependent, catches ordinary context) and the lines matching a
per-kind pattern **as they stream past** (position-independent, catches the
early race report). Each is bounded, so a report is a fixed size no matter how
loud the lane was. The test that pins it pushes a `DATA RACE` line, then
20 KB of filler, then a `--- FAIL:`, with a 256-byte tail — and asserts both
survive.

That is also what forced `lane-failure.mjs` to own the patterns rather than
the runner: the runner has to apply them while streaming, and the suite has to
test them without a Go toolchain anywhere near it.

## The dedup bug the fixtures found

`summarizeFailure` takes the notable lines *and* the tail. On any lane whose
whole output fits in the tail, every notable line is in both — so "2 issues"
rendered as `(+3 more)`. Caught by a test asserting the count, not by reading
the code.

The fix is a dedup in `evidenceLines`, but the lesson is the fixture shape:
the helper in `lane-failure.test.mjs` derives `notable` from the same string it
passes as `tail`, exactly as the runner does on a small lane. A fixture that
had supplied the two independently would have agreed with the implementation
and asserted nothing.

## A `try/finally` around an async body deletes the directory first

`withDir` in `run-lanes.test.mjs` was `try { return body(dir) } finally { rm }`.
With an async body that removes the directory the instant the promise is
*returned*, so a lane spawned with `cwd` set to it starts in a directory that
no longer exists. It surfaced as
`getcwd: cannot access parent directories` inside an assertion about a missing
binary — several layers from the cause, and it only fails for the tests slow
enough to still be running.

## What was deliberately left alone

- `make verify`. Untouched, and so is every lane's command: each one is the
  literal string the `ci.yml` step it replaced ran. That is what keeps
  `review-panel.mjs`'s `MECHANICAL_COVERAGE_NOTE` and `agent-command-verbs.md`
  §4b true without editing either — they are an inventory of what CI *proves*,
  and this change moves only how a lane is *reported*.
- One step per lane in `ci.yml`, rather than one step running the manifest.
  The reports are for the fixing agent; the Actions UI is for people, and
  collapsing eight named steps into "Run the CI lanes" takes the step name
  that says which check failed away from the humans to give a machine
  something it already had.
- The review panel's fix arm. Out of scope, and it reads CI's *conclusion*,
  not its logs.

## The guard that pins the two files together

The failure mode of a manifest is a lane nobody invokes: add it here, forget
the `ci.yml` step, and the finish step reports it as `skip` on every green run
forever — a gate that exists in the manifest and runs nowhere. The set
equality between the lane names in `LANES` and the `run-lanes.mjs <name>`
invocations in `ci.yml` is asserted in `run-lanes.test.mjs`, and the CLI
refuses an unknown lane name outright so a typo in the workflow is a red step
rather than a silent hole.
