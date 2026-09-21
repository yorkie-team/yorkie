# A style lands on a deleted text node in Go and not in JS

**Created**: 2026-09-21

Tracked as yorkie#2011 (JS mirror: yorkie-js-sdk).

## Problem

`canStyle` admits a removed node whenever the style's ticket happens to be
`After` the removal's:

```go
// pkg/document/crdt/rga_tree_split.go, and TreeNode.canStyle verbatim
return nodeExisted && (s.removedAt == nil || editedAt.After(s.removedAt))
```

The JS SDK instead refuses every removed node (`if (node.isRemoved()) continue`
in `CRDTText.setStyle` and `.removeStyle`). The issue framed this as a choice
between the two. Measuring says neither is right.

### The server does not converge with itself

A style concurrent with a removal is applied unconditionally on the replica
that issued it — the node is still live there, and nothing can retract it
afterwards. The replica that receives it runs it through `canStyle`, where the
`editedAt.After(removedAt)` comparison decides on a Lamport/actor-ID tie-break.
So the same two operations agree or disagree depending only on which actor ID
sorts higher.

Measured on `main`, two replicas seeded with `"abcdefghij"`, one styling
`[4,6)` while the other deletes it, exchanged through
`converter.ToChangePack`/`FromChangePack`:

| who styles | d1 | d2 |
|---|---|---|
| d1 (ticket loses) | `"ef" (removed) [b=1]` | `"ef" (removed) []` |
| d2 (ticket wins) | `"ef" (removed) [b=1]` | `"ef" (removed) [b=1]` |

The JS guard is unconditional, so JS diverges in *both* orderings, and the two
replicas also report different `docSize.gc` for the same document
(`{8,72}` vs `{4,48}`) — `MaxSizeLimit` is enforced client-side off that number.

### The rendered divergence from the issue

Six operations, single actor, no sync:

```
1 Edit(0, 0, "abcdefghij")
2 Style(4, 6, {b: "OLD"})
3 Edit(4, 6, "")          // tombstones "ef"
4 Style(0, 8, {b: "NEW"}) // Go styles the dead node too; JS does not
5 undo                    // reverses step 4
6 undo                    // restores "ef"
```

| step | Go | JS |
|---|---|---|
| 5 | gcLen **4** | gcLen **3** |
| 6 | `[{"val":"abcd"},{"val":"ef"},{"val":"ghij"}]` | `[…,{"attrs":{"b":"OLD"},"val":"ef"},…]` |

### The accounting half

`Text.DataSize` and `TreeNode.DataSize` both skip a removed node, so Live never
holds a tombstoned node's attributes — #2010 stopped charging Live for them.
GC is still wrong: `collect` subtracts `pair.Child.DataSize()` as it stands at
purge time, so a style that grows a node *after* its removal registered a
charge leaves registration and purge disagreeing.

```
seed "abcdefghij"; Edit(2,4,""); Style(0,8,{b:"1"})
live    Live{24,168} GC{4,48}
rebuilt Live{24,168} GC{8,72}
```

## Decision

**A style applies to every node the styling change knew about, and does not
ask whether that node has since been removed.**

`canStyle` reduces to one question:

```go
func (s *RGATreeSplitNode[V]) canStyle(vector time.VersionVector) bool {
	return ticketKnown(vector, s.createdAt())
}
```

### Why not "skip a removal the change already knew about"

That rule reads better on the six-step history — the restored `"ef"` keeps
`b="OLD"` — and it was the branch's first answer. It does not converge.

`removedAt` is last-writer-wins and **mutable**: `Remove` overwrites it when a
removal the node has not seen arrives with a later ticket. A style is
evaluated once, when it arrives. So the predicate's input depends on which
removals have landed, and two clients deleting the same run concurrently is
enough to break it. Three actors, all changes causally legal:

```
B removes "ef"                   (concurrent with C)
C removes "ef"                   (concurrent with B, later ticket)
X sees B only, then Style(0, 8)  (knows B's removal, not C's)
```

| delivery order | `"ef"` under the causality rule | under this one |
|---|---|---|
| `C,B,S` | `(removed) [b=1]` | `(removed) [b=1]` |
| `B,C,S` | `(removed) [b=1]` | `(removed) [b=1]` |
| `C,S,B` | `(removed) [b=1]` | `(removed) [b=1]` |
| **`B,S,C`** | **`(removed) []`**, `GC{4,48}` | `(removed) [b=1]`, `GC{8,72}` |

Neither storing more removal tickets on the node nor converging `Remove` on
the *earliest* concurrent tombstone repairs it:

- the replica cannot know which of the concurrent removals the styler had
  seen, because it may not hold that one yet;
- and the style can still be applied before the earliest removal arrives.

Any predicate over removal state is delivery-order dependent. The only
order-independent rule that does not change the wire format is not to read
removal state at all. (Expressing a local style as the live runs the user
actually selected would also work, and would keep the nicer undo semantics,
but that changes the operation's shape — see the follow-up note below.)

### What it costs

A style covers text the same client had already deleted, invisibly. Undoing
the style and then the deletion brings the text back **without** the
attributes it carried:

| step | before | after |
|---|---|---|
| 6 | `[…,{"attrs":{"b":"OLD"},"val":"ef"},…]` (JS today) | `[{"val":"abcd"},{"val":"ef"},{"val":"ghij"}]` |

That is the behaviour #2011 observed on the server and called a bug. #2011's
actual complaint was the *disagreement* between the two SDKs, and this closes
it — with the answer that converges rather than the one that renders better.
It is a behaviour change for JS SDK users, who had the skipping behaviour.

### The candidates, measured

Each patched in and run against the full unit suite and the four-order replay:

| contract | 2 replicas | 3 actors, 2 removals | six-step | ledger |
|---|---|---|---|---|
| `editedAt.After(removedAt)` (the old server rule) | diverges | diverges | `ef` plain | GC off |
| add the SDK's guard to the server | diverges | diverges | — | — |
| skip a causally-known removal | converges | **diverges** | `ef` keeps `OLD` | exact |
| **never read removal state** | converges | converges | `ef` plain | exact |

### The accounting half

A style landing on a tombstone grows a node whose GC charge was taken when it
was removed, and `collect` subtracts `pair.Child.DataSize()` as it stands at
purge time. So `docSize.GC` has to follow the node's size: `accAttrWrite`
books an attribute write on a tombstoned node to GC instead of dropping it,
which is what `Style` and `RemoveStyle` report through `resource.DocSize`.
#2010 fixed the Live half and named the GC half as a known limitation; this
closes it. Under this contract the local path reaches it too, so it is no
longer remote-only.

## Tasks

- [x] `canStyle` takes only the change's version vector and asks one question
      -- did the change know this node existed -- on `RGATreeSplitNode` and on
      `TreeNode`. `clientLamportAtChange` and `styleClientLamportAt` go with
      it: they were the same predicate spelled a second way
- [x] Correct the comment on the tree's `InsNextID` propagation loop: it walks
      siblings the style did not know about, which is now the only question
      `canStyle` asks
- [x] Route an attribute write on a tombstoned node through `docSize.GC`
      instead of dropping it, so registration and purge agree
- [x] Give an attribute its own size back when a revive un-registers its pair
      — found while reviewing the above, only reachable once a style can land
      on a tombstone
- [x] Convergence test: concurrent style vs removal, both ticket orderings,
      over the protobuf round trip, comparing tombstone attributes rather than
      rendered content
- [x] Ledger test: `TestRemoteStyleOnARemovedTreeNodeKeepsLiveExact` and
      `TestStylingOverATombstonedTextNodeKeepsLiveExact` are replaced by tests
      that assert GC as well as Live and then collect
- [x] Mirror all of it in the JS SDK

## Review

Green: `go test ./...`, `make lint`, `make test` (integration, MongoDB up).
Every new test was checked Red against `main` first.

Cross-SDK: the JS SDK's 2607 integration tests pass against a server built
from this branch, and the six-operation sequence from the issue now produces
the same document on both sides.

Two things this turned up that the issue did not name:

1. The server did not agree with itself. `editedAt.After(removedAt)` decided
   the concurrent case on an actor-ID tie-break while the issuing replica had
   already applied the style unconditionally, so "add the SDK's guard to the
   server" would not have converged either. Nor did the branch's own first
   answer, once a second concurrent removal was in play -- which is what
   settled the contract on reading no removal state at all.
2. `RegisterGCPair`'s un-register branch gave back the amount registration
   added, which is zero for an attribute removed from a node that was already
   a tombstone. Only reachable once a style can land on a tombstone at all,
   and it needs three changes concurrent with one removal.

### From code review

Three findings, two fixed here:

1. **The tree's `RemoveStyle` never got the third accounting case.**
   `attrGCPair` took only `wasLive`, so a live attribute on a tombstoned tree
   node was debited from Live — where it never was — and credited to GC twice.
   Live reached **-26** on the case the new test covers. Pre-existing, but the
   branch had just fixed the `Style` half of the same question; the two helpers
   are now one. Both SDKs.

2. **Two comments had `TreeNode.DataSize` where they meant `Tree.DataSize`.**
   The per-node function counts a live attribute whether or not the node is
   removed; the exclusion is the container's. That confusion is what justified
   leaving the tree at two cases, so the comments are corrected rather than
   reworded.

3. **Two concurrent removals of the same node make `canStyle`'s input
   delivery-order dependent** — not fixed, see below.

### Known limitations

**SDK version skew.** Clients apply remote changes with their own `canStyle`,
so an un-upgraded SDK on the same document as an upgraded one computes
different tombstone attributes and a different `docSize.GC` — and
`MaxSizeLimit` is enforced client-side off that number. The server and both
SDKs are one logical release. The skew is tolerable because the disagreement
is invisible until an undo, but it is real and embedded SDKs cannot be
force-upgraded. It is also a **behaviour** change for JS SDK users, who had
the skipping behaviour before.

**Change-log replay.** A server rebuilding a document from its full change log
with this code computes a different state for any history that styled a node
whose removal the style had already seen. Existing snapshots are unaffected —
changes applied on top of them keep whatever the old code decided — and no
wire format changed.

### Structural parity with the SDK

Two shapes that differed for no reason, straightened so a reader following one
implementation across to the other lands in the matching place:

1. `ticketKnown` was package-private in `crdt/tree.go` but is the causality
   primitive for `rga_tree_split.go` too. It now lives in
   `time/version_vector.go` as `time.TicketKnown`, where the SDK keeps it.

2. `RegisterGCPair` only added to GC; taking the same bytes out of Live was a
   second call — `AdjustDiffForGCPair` — that every caller had to remember,
   and forgetting it is not a compile error but silent drift only a rebuild
   can see. It had already cost `json.Tree.RemoveStyle` exactly that. The SDK
   has always done both halves in `registerGCPair`, using `gcOnlySize` to mean
   "add to GC, take nothing out of Live". Go now does the same and
   `AdjustDiffForGCPair` is gone, so the compiler finds every site.

   Folding it in surfaced four registrations that had been relying on the
   caller's silence to say "this was never in Live" and now have to say it
   with `GCOnlySize`: the array dead position node (`operations/move.go`,
   `json/array.go` ×2) and a node recreated under a removed parent
   (`crdt/tree.go`, born tombstoned and never reported as recreated). The
   snapshot-load scan says it through `GCPairs()`, which now marks its pairs
   the way the SDK's `getGCPairs()` always has.

   Measured after: an array move reports `live={12,144}` on both SDKs, where
   before it was 144 on the server and 120 in the SDK.

### Deferred

**The array move ledger.** Both SDKs now agree at `live={12,144}`, and both
are 24 bytes under the truth: a rebuild says `{12,168}`. `moveAfter` never
charges Live for the position node it creates. Separate from the GC-pair
question -- the running ledger is short, not the GC side -- and separate from
this branch's subject.

**Residual order-dependence, neither introduced nor fixed here.** Two
mechanisms survive the contract change, both measured identically on
`origin/main`:

- *Tree style traversal reachability.* The set of nodes a style's range
  traversal reaches depends on concurrent structural edits, which `canStyle`
  does not control. A 300-seed fuzz still diverges on 10 seeds, always on
  split or merged `<p>` nodes. The contract did shrink it: attribute-only
  divergence went `main` 54/300 → 33/300 → **10/300**, and text-only
  divergence to zero.
- *`RHT.Remove` carries the value it replaced.* A concurrent same-key style
  and removeStyle on a LIVE node, with no removal anywhere, leaves `b=s*` and
  `GC{4,24}` under one order and `b=LLLL…*` and `GC{34,24}` under the other.
  This is now the largest remaining source of attribute divergence and wants
  its own issue.

**Keeping the nicer undo semantics.** The cost of this contract is that a
style covers text the same client already deleted. The way to avoid it without
giving up order-independence is to stop expressing a local style as one
`(from, to)` range that sweeps tombstones, and express it as the live runs the
user actually selected. Then an already-dead node is outside every sub-range
on every replica — order-independent by construction — while a node removed
*concurrently* still sits inside a live sub-range and gets styled, which is
what convergence needs. That changes the operation's shape and its wire
encoding, so it is its own piece of work.
