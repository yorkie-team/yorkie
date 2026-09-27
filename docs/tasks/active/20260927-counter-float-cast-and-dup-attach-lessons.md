**Created**: 2026-09-27

# Lessons — Counter float deltas and duplicate attach

## Notes

- Go's out-of-range float-to-int conversion really does differ by CPU.
  Running the same Red table under `GOARCH=amd64` (Rosetta) on an arm64
  Mac showed it: `10 + 4294967296.5` on an Int counter gave `-2147483639`
  on arm64, `-2147483638` on amd64, and `10` in JS. Cross-compiling the
  test is a cheap way to prove an implementation-defined bug.
- Compute parity expectations by running the JS formulas in node, not
  by reasoning. `removeDecimal` truncates the delta *before* the float64
  add, so `-1 + 0.5` stays `-1`; truncating the sum would give `0`. And
  above 2^53 the float64 add rounds: `10 + 2^60` on an Int counter is `0`
  in JS, not `10`.
- JS's Int-counter add is not exact for |delta| >= 2^53, so concurrent
  huge deltas are order-dependent even in JS. Go mirrors it when it
  applies a JS Double delta, but the Go json layer sends an exact
  integer delta instead, which converges.
- The Red test for the crdt casts surfaced a larger gap: `negatePrimitive`
  had no `Double` case, so `Increase.Execute` failed outright on any JS
  fractional delta since the undo port (#1932, v0.7.17). Testing the
  operation layer, not only the crdt layer, is what found it.
- Keeping the Go json layer on integer deltas (not switching to `Double`
  like JS) matters for compatibility: servers from v0.7.17 on cannot apply
  a `Double` delta until this fix is deployed.
- `TryAttaching` in mongo is a filtered `FindOneAndUpdate`, so a miss
  cannot say why it missed. A second read only on the miss path gives a
  precise error without costing the success path a round trip.

## Review rounds

### Round 1 — correctness and tests

- BLOCKING, fixed: `Deactivate` closed watch streams but kept
  `attachments`, so after `Deactivate` → `Activate`, the new guard
  rejected attaching the same key, which used to work. JS
  `deactivateInternal` detaches every attachment. Red: new integration
  test "attach the same key after reactivation" failed; Green after
  `Deactivate` drops the attachments and marks them detached.
- Fixed: a transient mongo read error on the miss path was wrapped as
  `ErrClientNotFound`; it now surfaces as the read error.
- Accepted as known limitations (pre-existing or JS quirks):
  - Channels share the `attachments` map; the guard checks only document
    attachments, and a document can still overwrite a channel entry with
    the same key.
  - A failure after `attachments.Set` in `attachDocument` (watch loop,
    InitialRoot) leaves the entry. Revisited after review, see below.
  - The Go json layer sends an exact wrapped integer delta; JS adds in
    float64. They differ only for |delta| > 2^53, and each still
    converges.
  - An Int counter created with NaN errors in Go; JS stores NaN.
- Verified by the reviewer: `TruncFloatToInt64` matches node for ±2^63,
  ±2^63±2048, ±2^64, ±1e20, ±MaxFloat64, 5e-324, -0.9.

### Round 2 — design fit

- No blocking findings. Applied the non-blocking ones:
  - The guard now runs before `SetActor`, as in JS, so a rejected
    `Document` is left untouched.
  - The `attachDocument` doc comment had been split from its function.
  - Two comments were wrong: the guard's (the server no longer reports
    `ErrClientNotFound` after this PR) and the json layer's (old servers
    do apply a Double delta; they fail to negate it and convert it
    CPU-dependently).
  - `Deactivate` resets a channel's session count, as `detachChannel`
    does.
  - A NOTE that the mongo miss read is not atomic with the update; only
    the reported reason can be off under a race.
- Follow-up outside this repo: the JS comment near `attachDocument`
  saying the server reports `ErrClientNotFound` goes stale once this
  server ships.
- Integration runs collided on port 11201 with another session's test
  binary. Check `lsof -iTCP:11201` before reading a bind failure as a
  test failure.

### Coordinator follow-up

- The server failure was reproduced end to end, not just in a unit test.
  `TestCounterDoubleDeltaFromJS` makes a raw RPC client push 11 Increase
  changes with a Double delta, one more than the snapshot threshold. On
  `main`, the server's replay (`pullSnapshot`, also the background
  snapshot) fails with `internal: not applicable datatype`. So c1's
  PushPull fails, and c2's AttachDocument fails. It passes on this branch.
- Defence in depth: `Increase.Execute` now builds the reverse only when
  `source.NeedsReverse()`, as Remove, ArraySet, Move and Edit already do.
  A remote apply or a server replay never builds a reverse it would throw
  away, so an unbuildable reverse cannot fail an apply again.
- "A post-Set failure leaves the entry" is not a regression, and removing
  the entry would be worse:
  - `attachDocument` sets the Document to Attached before
    `attachments.Set`. On `main`, retrying with the same Document was
    already `ErrNotDetached`.
  - The server has already attached the key, so a retry with a new
    Document was rejected there (`ErrClientNotFound` on `main`).
  - The entry is what lets the caller `Detach` the Document and release
    the server-side attachment. Dropping it would orphan that attachment,
    and the retry would still fail on the server.
  - JS also records the attachment before `runWatchLoop` and does not
    remove it in its `catch`.
- What this PR did introduce is the in-flight mark, and every error path
  releases it through `defer`. The new "retry a failed attach" test
  covers two failures: a cancelled context, and the server rejecting an
  unknown schema. After either, the same key attaches again, with the
  same Document or a new one. Mutation check: dropping
  `defer c.endAttach` fails the test.

## Review round 2 (panel: blast-radius, security, correctness)

- **A non-finite delta had to stop erroring, not error better.** All
  three lenses converged on the same hole: `crdt.Counter.Increase`
  returned `ErrNonFiniteNumber` on *every* source, and `pushPack` stores
  a pushed change before executing it, so one `NaN` Increase from a raw
  client permanently broke replay/snapshot/attach for that document —
  the exact poison-pill shape this PR set out to fix. Gating the reverse
  (`NeedsReverse`) was not enough, because the apply itself failed.
  `Increase` now drops a NaN/±Inf delta and returns the counter
  unchanged, so the apply is total and every replica converges on the
  same value. Local misuse still fails loudly: `json.Counter.Increase`
  panics with `ErrNonFiniteNumber` before the operation is ever created.
  Rule of thumb: any error the *replay* path can raise for data the
  server already stored is a durability bug, not validation.
- **A guard over a shared map must cover every writer of that map.**
  `attachments` is keyed by `key.Key` alone and holds both Documents and
  Channels, so gating only `TypeDocument` left `Attach(channel "k")`
  free to replace a Document's entry — after which the Document was
  orphaned and a second Document with that key attached cleanly.
  `beginAttach` now runs for every attachable type and rejects any live
  entry under the key.
- **Teardown that deletes state breaks readers; marking it does not.**
  Deactivate deleting every attachment turned "deactivate, reactivate,
  keep the handle" into `ErrNotAttached` at every reader. It now only
  marks resources detached (which is what lets the key be attached
  again), and `beginAttach` evicts the stale entry when the key is
  reattached.
