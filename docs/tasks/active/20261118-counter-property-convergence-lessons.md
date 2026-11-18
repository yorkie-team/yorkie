# Lessons — Property-based convergence tests for Json.Counter

## Context

Issue #1894, part of #1862. Test-only change adding
`test/integration/counter_property_test.go`.

## Notes

- The three existing property tests (object/array/text) share one shape:
  per-replica random op bursts, then a single
  `syncClientsThenAssertEqual`. Counter needs sync *inside* the generated
  sequence, because where a sync lands decides which increments are
  concurrent. So this test keeps their seed-per-replica reproducibility
  convention but models the run as an explicit event list instead of
  "ops then sync".
- Making the run return a violation string instead of calling
  `assert`/`require` is what makes minimization possible at all: a shrink
  loop has to replay candidate sequences and ask "does this still fail?",
  which it cannot do if the first failure aborts the test. Infrastructure
  errors (attach/sync/detach) still use `require.NoError` — they are not
  properties and should abort.
- `helper.TestKey(t, prefix)` takes an int prefix, which gives each
  replay attempt its own document key. Without that, shrink replays would
  reuse a document that already carries the previous attempt's
  increments.
- Counter convergence is a strong property to assert exactly: unlike
  Array or Text, the expected final value is a plain sum, so P2 catches a
  lost or duplicated increment that P1 (all replicas equal) would miss if
  every replica lost it the same way.

## Self review

Not run: this autonomous run is not granted the reviewer subagent that
`/self-review` dispatches. The branch is handed over as a draft PR for
CI, `@claude review`, and human review.

## Verification

- `make lint` — green (includes the `integration` build tag lane).
- `go test -tags integration -run TestCounterPropertyConvergence
  ./test/integration/` — green, all 10 seeds, also under `-race`.
- `go test -tags integration -run TestCounter ./test/integration/` —
  green, so the new file does not disturb the existing Counter tests.
- The shrink path was exercised deliberately: temporarily skewing the
  expected total by one whenever a candidate had >= 3 events made the
  property fail, and the report came back minimized from 16 events to
  exactly 3 — the smallest sequence that still trips that injected
  fault. The injection was reverted before committing. Worth doing: a
  minimizer that is only reached on failure is otherwise dead code that
  nobody notices is broken until the day it matters.
- Not verified here: the rest of the integration lane and the complex
  lane. CI runs those.
