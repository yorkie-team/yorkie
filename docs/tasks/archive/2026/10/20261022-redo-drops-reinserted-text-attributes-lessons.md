# Lessons — Redo after GC drops the attributes of re-inserted text

## What the bug taught

A span set that is only ever read in one direction today becomes bidirectional
the moment the reverse of the reverse exists. `retombstoneSpans` was built for
re-removal, where the payload is ignored, but undo/redo flips the direction, so
the same bytes get read as restore content. Any field skipped as "irrelevant
for this direction" is a latent divergence once the direction flips.

The divergence needs three replicas' worth of state to show up — a GC'd
replica and a non-GC'd one — because `RGATreeSplit.restore` has two arms: it
un-tombstones a surviving piece (attributes intact, span ignored) and recreates
a purged piece from the span. The two arms must agree on what the node is, or
collection becomes observable.

## A snapshot is only correct at the instant it is taken

Carrying the edit's own `attributes` on the inserted span fixes the insert's
own styling, but the span then sits on the undo stack while the document keeps
moving. A `Style` from another actor lands on the node, never on the span, so
by the time the undo re-removes the run the span describes a state that is
hours old. The fix is to re-read the attributes off the chain at the moment
the reverse is built — the same instant `Text.Edit` reads them when it fills
`removedSpans` — rather than to trust what was recorded at insert time.

The symmetry is worth keeping in mind: for any span the reverse will later
restore, the attributes must be read *immediately before* the content is
tombstoned, because a tombstone is the last place those attributes exist on a
replica that has not yet collected it.

## Review rounds

- `/self-review` was not run: this is an autonomous one-shot run with no tool
  that can launch the reviewer subagent. The branch's reviewers are CI,
  `@claude review` and a human on the PR. No round should be read as clean.
