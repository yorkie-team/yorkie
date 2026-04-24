**Created**: 2026-04-24

# splitLevel>=2 Convergence — Lessons

## Clone/root consistency is a mandatory check

`syncClientsThenAssertEqual` only compared Marshal() (JSON) across
documents (d1 vs d2). It did not compare clone vs root within the same
document. This allowed Fix 15 to achieve d1==d2 convergence by making
both roots diverge from their clones in the same way — 308 hidden
mismatches.

**Rule**: Always verify `doc.Root().GetTree(key).ToXML()` matches
`doc.RootObject().Get(key).(*crdt.Tree).ToXML()` after sync.

## VV-gated code must not change tree structure vs clone

Any code that runs only when `versionVector != nil` changes the root
path but not the clone path. If this code modifies tree structure
(not just position resolution), root and clone will diverge.

Fix 15's original approach (`left = splitSibling` only when VV is
non-nil) violated this. The replacement (`skipActorID`) filters
correctly without introducing root-only structural changes.

## Same-actor siblings are not concurrent

`advancePastUnknownSplitSiblings` uses VV to detect concurrent
siblings. But `issueTimeTicket` creates tickets with lamports beyond
the change's VV, making the current operation's own split products
appear "unknown". The `skipActorID` parameter prevents this false
positive. A single actor's changes are sequential, so an "unknown"
sibling from the same actor is always our own creation.

## Convergence tests can pass with wrong state

d1==d2 convergence does not guarantee correctness. Both documents
can converge to the same wrong state (root diverged from clone).
The clone/root check catches this class of bug.
