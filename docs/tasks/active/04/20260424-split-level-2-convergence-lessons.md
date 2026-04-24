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

## SplitElement cannot fix cross-level divergences alone

When `splitLevel-1` and `splitLevel-2` run concurrently on the same
node, the `splitLevel-1` client never executes a parent-level
`SplitElement`. Any fix inside `SplitElement` (partitioning logic)
only runs on the `splitLevel-2` side. The other client has no
opportunity to apply the same redistribution.

Fix 17 solved this by operating at the `Split()` level — the insertion
point — which runs on BOTH clients regardless of `splitLevel`. When
a replay split creates an empty sibling and the original node's
existing InsNext sibling is in a different parent, the empty sibling
is moved to that parent.

## Structural fixes over VV-gated fixes

Fix 17 uses no VV checks. It relies purely on tree structure
(`InsNext` parent comparison + empty children check). This avoids
clone/root divergence by construction. Prefer structural invariants
over VV-gated logic when the fix can be expressed structurally.

## Separate delete range from insertion point

Fix 18 initially modified `fromParent`/`fromLeft` for both
`collectBetween` (delete range) and the insert step. This fixed the
delete divergence but moved the insertion point to the wrong parent
(split sibling instead of original). On the other replica, the
insert happened before the split and Fix 16's boundary migration
handled placement.

**Rule**: When narrowing a cross-parent traversal range, use
separate variables for `collectBetween`. Preserve the original
`fromParent`/`fromLeft` for merge, split, and insert steps so the
content lands where the editor intended.

## Text split IDs cross canDelete boundaries

Text split nodes share `CreatedAt` with the original text, making
them "known" to any client that knew the original. A concurrent
element split places text("cd") — split from "abcd" — inside a new
element split sibling. The delete traversal picks up this text via
the cross-parent range and `canDelete` returns true because the
`CreatedAt` is known. The fix must narrow the traversal range (not
weaken `canDelete`), and must be VV-independent because clone also
sees the same `CreatedAt`.
