---
title: split-boundary-insert-side
target-version: 0.7.26
---

# Which Side of a Split Boundary an Insert Lands On

## Problem

Text inserted at exactly the position where a peer concurrently splits the
paragraph lands on the left of the new boundary on one replica and on the right
on the other (yorkie-js-sdk#1436). Three operations are enough; both replicas
hold `<doc><p>ab</p></doc>`:

```go
d1: tree.Edit(2, 2, &json.TreeNode{Type: "text", Value: "e"}, 0) // "aeb"
d1: tree.Edit(2, 2, nil, 1)                                      // split "a|eb"
d2: tree.Edit(2, 2, nil, 1)                                      // split "a|b"
// d1: <doc><p>a</p><p></p><p>eb</p></doc>
// d2: <doc><p>a</p><p>e</p><p>b</p></doc>
```

The replica that applied the insert first resolves the split against a tree
where the text is already there. The replica that applied the split first has
the right half in the product, so the insert resolves at the end of the left
piece. Each is self-consistent; the two disagree.

Two rules of [concurrent-merge-split](concurrent-merge-split.md) pull against
each other here. §7.3 (Boundary Insert Migration) keeps a concurrent insert at
a split boundary on the *left* of it. §7.8 orders same-boundary split products
newest ticket first. Where a concurrent product begins with inserts the
splitter never saw, both cannot hold.

yorkie-js-sdk#1467 resolves this in the JS SDK. Where a same-boundary split
lands is a replicated contract -- the server and every SDK must pick the same
node for the same change -- so this document records the Go port of the same
two rules, made for parity with that PR: first at `89b0b2a8`, then with the
three review fixes described under
[Order-independent gates](#order-independent-gates) that the JS PR takes too.

### Goals

- The minima of yorkie-js-sdk#1436 converge in Go, in tree shape (node IDs)
  and in XML, with the same converged XML the JS SDK produces.
- Every gate the two rules apply reads only state that is the same on every
  replica whatever the delivery order and GC timing: the change's version
  vector, node tickets and `removedAt`.
- No regression in the existing split-ordering and split-cascade suites.

### Non-Goals

- Changing where a *range* endpoint resolves. Phase 2's
  `advancePastUnknownSplitSiblings` (§7.5) stays the only rule that moves range
  endpoints past split products; style targets (§9.4) and delete ranges are
  untouched.
- Departing from the JS rule, even where it is known to be wrong. See
  [Remaining divergences](#remaining-divergences).
- Converging every insert/split interleaving. yorkie-js-sdk#1436 has cases
  this rule leaves open; they are recorded, not fixed, here.

## Design

Both rules live in `pkg/document/crdt/tree.go`, named after their JS
counterparts, and are two views of one claim: **when the right piece of a
concurrent split begins with a run of inserts the splitter did not know, the
two boundaries are not the same boundary**, so §7.8's ticket order does not
decide between them -- content order does.

### Split applied second (`orderSameBoundarySplit`)

§7.8 walks the `InsNextID` chain to order same-boundary products by ticket.
When the product sitting next is older than the split but begins with
concurrent inserts, the split lands *inside* that product, past the run,
instead of in front of it. When the walk steps over newer products, the last
of them is split past its own run the same way, rather than at offset 0.

`boundaryInsertRunOf` counts the run: *text* children at the start of a node
whose ticket `time.TicketKnown` reports as outside the editor's version
vector. Text split siblings carry their original's ticket and so end the run
by being known, and the first element child ends it. Text-only is also the
only GC-stable measure: telling an element insert from an element split
product would need `InsPrevID`, which `Tree.Purge` relinks and clears and
`DropSplitLinks` drops on a copy.

`advanceIntoSplitProducts` calls the same function on the other side of the
boundary rather than scanning for itself, so the run cannot be measured two
ways: the split places its product after exactly as many children as the
insert walks over.

### Insert applied second (`advanceIntoSplitProducts`)

The step 04 RGA scan of `FindTreeNodesWithSplitText` stops at the last child of
the resolved parent, but the sequence continues into a concurrent split product
of that parent. `advanceIntoSplitProducts` carries the scan on there, over the
product's leading run of concurrent *text* children -- the same rule step 04
applies inside one node, and the same run `boundaryInsertRunOf` measures.
Element children at that boundary are §7.8's business; crossing them made the
two rules disagree in the JS SDK and broke eight split-ordering tests.

"The last child" is measured in content the editor saw as live
(`atEndOfLiveContent`), not in all children, following the JS rule.

It runs in `Edit` right after Phase 1, not inside
`FindTreeNodesWithSplitText`: that method also resolves style and delete
ranges, and an endpoint that walked into a product would change which parents
`collectBetween` runs between, over nodes the editor never saw.

Three conditions at that call site keep it to the one anchor it is about:

- A **collapsed** range, and both endpoints are moved together. Moving one
  would point the traversal from the product back at the node it came out of.
- **Content and no split level.** `Edit(i, i, nil, n)` -- plain Enter -- is a
  collapsed range too, and Phase 7 splits at `fromParent`/`fromLeft`, so an
  advanced anchor would open the new boundary inside someone else's product
  instead of at the position the editor named. §7.5/§7.8 would then be
  comparing two boundaries that are not the same one. Seed 3768 of
  [Remaining divergences](#remaining-divergences) was exactly this, and
  converges with the gate in place.
- **Not an anchor §1.1 redirected** off a parent a concurrent merge
  tombstoned (`declaredMergeSource`). That redirect returns a child of the
  merge destination, which `atEndOfLiveContent` cannot tell from a step 04
  result, and both §9.4 stamping (`intendedMergeParent`) and the §6.2
  declared-boundary skip (`propagateMergeDeletes`) key off `fromParent` being
  that destination -- a `fromParent` moved onto a split product of it stops
  both silently.

### Order-independent gates

The port at `89b0b2a8` had gates whose answer depended on what had arrived on
the replica, not on what the change knew. Review of yorkie-js-sdk#1467 found
three of them, and both SDKs apply those fixes; review of this PR found the
rest, recorded below with them.

**The end of live content counts only known removals.** `atEndOfLiveContent`
skipped every tombstone after the anchor. Whether a node is a tombstone when an
insert arrives depends on delivery order. Take `<p>acb</p>`: d1 inserts `r`
after `c` and splits right after `c`; d0 inserts `u` between `a` and `c`; d2
removes `c`. A replica that applied the removal before the insert saw `a` as
the end of live content and carried `u` past `c` into the product, while the
typist kept it in the paragraph:

```
typist:            <doc><p>au</p><p>rb</p></doc>
removal first:     <doc><p>a</p><p>rub</p></doc>
```

Now a trailing child counts as gone only when
`removedAt != nil && TicketKnown(versionVector, removedAt)`. The change's
version vector and the node's `removedAt` are the same on every replica, so
the answer no longer depends on arrival order. GC does not change it either:
a tombstone is purged only once its removal is known everywhere, so every
change applied after the purge would have counted it as gone anyway.
`advanceIntoSplitProducts` takes the version vector from `Edit` for this.

The walk's own liveness gate reads the same way. It stops when the product it
would continue into, **or any ancestor of it**, was removed as far as the
editing change knew (`removedSubtreeKnownTo`) -- the ancestors because
`sharesSplitFamilyParent` deliberately accepts a sibling under the next
level's product at a multi-level split, and `TreeNode.IsRemoved` answers only
for the node itself. A *concurrent* removal does not stop it: the replica that
applied the insert first put the content inside the product too, and the
delete then tombstones it on both, so advancing is what converges.

`orderSameBoundarySplit`'s liveness gate reads `removedSubtreeKnownTo` too,
for the same reason and so that the two sides of one boundary judge removal
alike: a local `IsRemoved` would make the split fall back to `parent` on the
replica that already holds a concurrent removal and step into the sibling on
the one that does not, placing the same product in two places.

Because every gate asks what the change knew, a change carrying **no** version
vector gets the most permissive answer to all of them, while
`orderSameBoundarySplit` -- the rule this one has to agree with -- switches
itself off. `advanceIntoSplitProducts` returns its argument unchanged for an
empty vector, so neither side of the boundary moves without one.

**Both run measurements cross only children the split moved.**
`advanceIntoSplitProducts` and `boundaryInsertRunOf` measure a run at the
start of a split product. Text typed into the product *after* the split also
sits there and is also unknown to a concurrent change, but it was never at
the original boundary. Counting it broke Enter-then-type: d1 presses Enter
after `a` and types `s` at the start of the new paragraph while d0 types `u`
after `a`. The replica that applied the split first scanned over `s` and
carried `u` into the new paragraph; the other kept it on the left
(`<p>au</p><p>sb</p>`, which `main` produces). `movedBySplit` stops both runs
at the first child younger than the product. Ticket comparison is stable under
delivery order and GC alike.

**A redirect only into an adjacent product.** When `orderSameBoundarySplit`
redirects a split past the boundary run of the next older product, that
product has to start at our boundary. If the walk stepped over a newer
product that still holds content past its own run, the next product was split
off that one at a different boundary, and its leading run has nothing to do
with ours. With a typist's insert and split after `a` and two concurrent
splits at the paragraph start, one replica split the typist's product past
`u` and diverged. The redirect now requires `target == parent` or a
stepped-over product holding nothing past its run.

The walk's fall-through return -- a split placed inside the last newer product
it stepped over -- needs the same condition, one step earlier: that product's
leading run is ours only if our boundary reached its start, which is the
adjacency of the product stepped over to get there. `orderSameBoundarySplit`
carries both as it walks (`adjacent` for the in-loop redirect,
`atTargetStart` for the fall-through) and falls back to §7.5's plain offset 0
when the chain was not adjacent.

### Remaining divergences

A two-replica fuzz of the JS tree (inserts and splits on `<p>ab</p>`, changes
delivered one at a time in random interleavings, trees compared by node ID)
still finds divergent runs after these fixes. Seven delta-debugged minima are
in `TestTreeSplitBoundaryRemainingDivergences`; six are still skipped. Every op
in each is concurrent with the other replica's, and the Go replicas end in the
same two states as the JS ones, node IDs included, so these are gaps in the
shared rule and not port differences.

| Case | Ops (d1 = replica 0) | Diverges in | `main` |
|------|----------------------|-------------|--------|
| seed 101 | d1 ins(2,c) ins(3,d) split(3); d2 split(2) | XML | diverges |
| seed 235 | d1 ins(3,c); d2 split(3); d1 ins(4,d) split(4) | XML | diverges |
| seed 193 | d1 ins(3,e); d2 ins(3,f) split(3) split(5) | XML | diverges |
| seed 24 | d1 ins(3,c) ins(4,d) split(5); d2 split(3) | node IDs | diverges |
| seed 69 | d2 ins(1,f) split(1) ins(3,h); d1 ins(1,i) | XML | diverges |
| seed 502 | d2 ins(3,g) split(3); d1 ins(3,h); d2 ins(6,i) | XML | diverges |
| seed 3768 | d2 split(3) ins(3,d) split(3) ins(6,e); d1 split(3) | fixed | converges |

The first four are not this rule's: they diverge identically on `main`, at
`89b0b2a8`, and with the same-boundary walk fix of yorkie#2098
(yorkie-js-sdk#1435) applied on top. That fix ends the §7.8 walk at the
product holding the right half; neither SDK has it on `main` yet, and these
cases do not depend on it.

Seeds 69 and 502 come from `movedBySplit`: without it they converge, and with
it Enter-then-type diverges. Enter-then-type is the common editing pattern, so
the filter stays; both diverge on `main` too. Seed 3768 came from the same
filter, but its last op is a plain split, and it converges now that Phase 1-0
leaves a pure split's anchor alone -- so no case left open here converges on
`main`.

### Risks and Mitigation

| Risk | Mitigation |
|------|------------|
| A JS replica and a Go/server replica place the same split differently | This port; the JS cases are translated one-for-one into `tree_boundary_insert_side_test.go` with the converged XML asserted |
| A gate reads replica-local state and diverges by delivery order | Every gate reads the version vector, tickets or `removedAt`; the trailing-tombstone, Enter-then-type and three-split cases are tested in several delivery orders |
| The rule leaves some insert/split interleavings divergent | Recorded as skipped subtests with both replica states, so a later fix changes both SDKs together; every one left open diverges on `main` as well |
| An `InsNextID` that did not come from `SplitElement` redirects a split or an insert | `sharesSplitFamilyParent` keeps both walks inside one split family; `insNextWalker` stops a cyclic chain |
| Splitting a sibling the editor saw removed makes the product born tombstoned inside an invisible subtree | Both walks stop at `removedSubtreeKnownTo`, the same test on both sides of the boundary |

### Design Decisions

| Decision | Reason |
|----------|--------|
| Port the JS decisions exactly, remaining divergences included | Parity is what convergence across SDKs needs; a Go-only fix is itself a divergence |
| Count a removal as gone only when the change knew it | A local tombstone depends on delivery order; the version vector and `removedAt` do not |
| Keep `movedBySplit` despite seeds 69 and 502 | Without it Enter-then-type diverges, which is far more common than three concurrent splits around an insert |
| Cross text children only, in both rules | Element children at that boundary are §7.8's, and text is the only GC-stable measure of the run |
| Apply the insert-side rule in `Edit`, to a collapsed range carrying content, no split level, and no merge redirect | A range endpoint moving into a product would widen or shorten what the edit deletes and merges; a split level would open its boundary inside someone else's product; a redirected anchor would silently disable §9.4 stamping and the §6.2 declared-boundary skip |
| Measure both runs by calling one function | Two scans of the same run can disagree, and the split would then land on a different side of the insert than the insert does of the split |
| Do nothing at all for an empty version vector | Every gate asks what the change knew, and "nothing" is the most permissive answer to each, while `orderSameBoundarySplit` switches itself off |
| No split-position measurement change | The JS PR also moves where the split's `TreeChange` position is measured; the Go tree emits no such change, so there is nothing to port |

## Alternatives Considered

| Alternative | Why not |
|-------------|---------|
| Leave Go on §7.8 ticket order | A JS replica on the new rule and a Go/server replica would place the same split differently |
| Fix the trailing-tombstone gap in Go only | Same reason; the fix landed in both SDKs together instead |
| Count the run by `InsPrevID` instead of tickets | Purge relinks and clears it and `DropSplitLinks` drops it, so the run would depend on GC timing |
| Resolve inside `FindTreeNodesWithSplitText` for every range | Style and delete ranges resolve through it, and their endpoints must not follow content across a boundary |

## Tasks

Track execution plans in `docs/tasks/active/` as separate task documents.
