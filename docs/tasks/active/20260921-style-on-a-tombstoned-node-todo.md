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

**A style skips a node whose removal the styling change already knew about,
and applies to one removed concurrently.**

- Locally (`len(versionVector) == 0`) every removal is known, so a user never
  styles text they have already deleted. That is the JS SDK's current
  user-visible behaviour, and it stays.
- A concurrent removal is not known, so the style applies on every replica —
  which is the only way the replicas can agree, because the styling replica
  already applied it while the node was live.

Measured against the two alternatives (each patched into Go and run against
the full unit suite):

| contract | concurrent convergence | six-step result | local ledger |
|---|---|---|---|
| drop the `removedAt` clause (always style) | both orderings | Go's current | still off |
| add the JS guard to the server | neither ordering | — | — |
| **skip a causally-known removal** | both orderings | JS's current | exact |

The concurrent case still lands a style on a tombstone — that is what
convergence requires — so the GC charge has to follow the node's size either
way.

## Tasks

- [x] `canStyle` takes the change's version vector and skips a causally-known
      removal, on `RGATreeSplitNode` and on `TreeNode`
- [x] Correct the comment on the tree's `InsNextID` propagation loop: a split
      sibling whose *creation* the style did not know cannot have a known
      removal either, so that loop needs no separate filter
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
   server" would not have converged either.
2. `RegisterGCPair`'s un-register branch gave back the amount registration
   added, which is zero for an attribute removed from a node that was already
   a tombstone. Only reachable once a style can land on a tombstone at all,
   and it needs three changes concurrent with one removal.

Known consequence, not addressed here: a server rebuilding a document from
its full change log with this code computes a different state for any history
that styled a node whose removal the style had already seen. Existing
snapshots are unaffected — changes applied on top of them keep whatever the
old code decided — and no wire format changed.
