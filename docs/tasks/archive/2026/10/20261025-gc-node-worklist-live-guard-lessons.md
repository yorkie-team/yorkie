# Lessons: the node GC worklist has no guard for a cleared removal

## A registered GC pair is a claim, not a fact

Both worklists in `Root.collect` treat registration as proof the child is a
tombstone, and two separate operations depend on it: `EqualToOrAfter` derefs
the ticket, `Purge` unlinks the node. The element loop already carried that
reasoning in a comment; the node loop did not, even though the node side is
the only side where `removedAt` is ever cleared in place today
(`TreeNode.unremove`, `RGATreeSplitNode.SetRemovedAt(nil)`). The invariant
held only because every reviver remembered to call `UnregisterGCPair` — which
is not a compile error to forget.

## Skipping and deleting are not interchangeable

The obvious "clean up the stale entry" reflex is wrong here. `RegisterGCPair`
moved the child's bytes Live→GC; a plain `delete` from `gcNodePairMap` leaves
`docSize.GC` holding them forever, and `MaxSizeLimit` reads `Live + GC`. Only
`UnregisterGCPair` (which also credits back the `removedAt` ticket the live
node no longer carries) or a real purge may take them out. Keeping the entry
turns an unpaired revive into a bounded over-charge instead of a crash or a
permanent ledger skew.

## Self review

Not run: this task was implemented in an autonomous headless run with no
reviewer subagent available. Review is left to CI, `@claude review`, and a
human — recorded here so the skipped round is not mistaken for a clean one.
