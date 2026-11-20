---
title: insertion-origins
target-version: 0.8.0
---

# Insertion Origins for Text and Tree

## Problem

Concurrent insertions at the same position can interleave (#2309). When one
user types `abc` and another types `가나다` at the same caret, replicas
converge, but they can converge to `a가b나c다`. Neither user typed that.

RGA places a new node after its left neighbour and orders concurrent siblings
by `createdAt`. Each character of a run is a child of the previous one, so the
two runs are siblings only at their first characters. After that, the
`createdAt` order of the remaining characters decides the order, and it mixes
the runs. This is the known weakness of left-origin-only sequence CRDTs. Yjs
(YjsMod) and Fugue (FugueMax) fix it by also recording a right origin.

### Goals

- Concurrent runs at the same position stay contiguous, for Text and for the
  children of one Tree element.
- Convergence is unchanged, and so is GC: collected tombstones still free
  their content.
- The Go engine and the JS SDK use the same rule, on the same wire fields.

### Non-Goals

- Choosing which run goes first. Ties are broken by actor, as before.
- Fixing divergence in concurrent Tree merge/split that exists without
  origins (see [Concurrent Merge and Split](concurrent-merge-split.md)).

## Design

### Origins

A local insert records two origins:

- **Left**: the boundary it was inserted at, in canonical form. For Text this
  is "after character `(C, k-1)`", written `(C, k)`. For a Tree it is the
  previous sibling's ID for an element, `(C, k)` for text, or nil at the start
  of the parent.
- **Right**: the ID of the node right after the caret at insert time,
  tombstones included. Inside a text piece this is the next character of the
  same insertion. It is nil at the end of the text or parent.

A node continuing an insertion after a split keeps the insertion's right
origin and uses its own start as the left origin.

### Placement (receiver)

The receiver resolves both origins. Only nodes between the left and right
origins can be concurrent with the insert, so it scans that window. Siblings
with the same left origin are ordered by their right origins, and then by
actor (the FugueMax rule). A sibling whose left origin resolves outside the
window ends the scan. If the window is empty, the node goes right after the
left origin.

Nodes without origins, from older clients or element split products, keep the
`createdAt` rule. In a Tree the scan steps over a split product and stops at
any other origin-less node.

For Trees, the scan runs over one parent's child list. A right origin that
moved to another parent through a concurrent split or merge is treated as "no
right bound in this parent" (`integrateTreeOpen`). Splits record a right origin
too and cut right before it. An insert into a merged-away parent, or after a
split it did not see, is placed by origin in the parent that now holds the
boundary.

### Skeleton GC

Placement reads tombstones: a right origin can name one, and the window
includes them. A replica that has purged a tombstone would place an insert
differently from one that has not. So GC no longer unlinks a collected node.
It keeps it as a **skeleton**:

- Text: the value and attributes are dropped. The ID range `[offset, offset+len)`,
  the origins and the node's place in the chain stay. Adjacent skeletons from
  one insertion merge into one node.
- Tree: a text node keeps its length. An element keeps an empty shell, and its
  removed descendants are detached.
- A skeleton is removed in every view and is never removed or styled again.
  All skeletons share one stand-in removal time, so a snapshot does not carry
  it. The removal ledger goes with the content.
- Undo of a collected deletion refills the skeleton in place. It records the
  same stand-in removal and cancel as a recreated piece.

### Wire format

| Message | Field | Number |
|---|---|---|
| `Operation.Edit` | `has_origin`, `origin_left`, `origin_right`, `origin_right_at_from` | 11–14 |
| `TextNode` | `has_origin`, `origin_left`, `origin_right`, `origin_flags` | 7–10 |
| `TextNode` | `skeleton_len` | 11 |
| `Operation.TreeEdit` | `has_origin`, `origin_right` | 12, 13 |
| `TreeNode` | `has_origin`, `origin_left`, `origin_right` | 11–13 |
| `TreeNode` | reserved for origin flags | 14 |
| `TreeNode` | `skeleton_len` | 15 |

`Edit` omits `origin_left` when it equals `from`, and `TreeEdit` never carries
one: every replica recomputes it from `from`. In a snapshot, `origin_flags`
omits origins that are a node's neighbours, which keeps snapshots of typed runs
close to their previous size.

### Compatibility with older clients

An older client ignores the new fields: protobuf skips unknown fields, so it
places every insert by `createdAt`. A newer client places an insert by origin
only when the insert carries one. These cases were checked by exchanging
change packs and snapshots between an engine built from `main` and one built
from this change:

| Case | Result |
|------|--------|
| One new and one old client type concurrently | Both converge to the old, interleaved order: the old client's insert has no origin, so the new client falls back to `createdAt` for it. |
| Two new clients type concurrently; an old client receives both | **Diverges.** The new clients converge to `X가나다abcY`, while the old client builds `X가나abc다Y` (forward) or `Xa가b나c다Y` (held caret). The divergence is permanent. |
| An old client loads a snapshot holding a skeleton | It loads, and the text matches. The skeleton becomes a live, empty node, and the old client's GC never collects it. |

So a document that newer clients edit must not be opened by older ones. The
server sees each client's SDK version (`x-yorkie-user-agent`) but enforces no
minimum. Two ways to close this, neither of them implemented here:

- **Per-document switch.** A document records whether it uses origins. Clients
  record origins only in such documents, and the server refuses to attach
  clients older than the version that understands them.
- **Project-level minimum SDK version.** A project setting the server enforces
  on attach. It is simpler, but every document in the project moves at once.

### Risks and Mitigation

| Risk | Mitigation |
|------|------------|
| Skeletons keep metadata that GC used to free | Only the content goes; adjacent skeletons merge. Skeleton compaction is follow-up work. |
| Older clients diverge from newer ones in the same document | See [Compatibility with older clients](#compatibility-with-older-clients). It needs a gate before release. Tests play an older client's operations with `RecordInsertOrigins` and `TreeOrigins`. |
| Tree divergence under concurrent merge/split | Origins reduce it (fuzz: 312 to 281 of 20,000 runs with GC) but do not remove it. A split boundary keeps an insert placed by origin after the element it cuts on the right of the cut, where a replica that saw the insert first put it. |

### Design Decisions

| Decision | Reason |
|----------|--------|
| Immediate right neighbour, tombstones included | It needs no per-node bookkeeping. A receiver that collected it recovers it from the change's version vector. |
| Skeletons instead of pinning tombstones | A pinned tombstone keeps its content forever. A skeleton keeps only what placement reads. |
| Left origin recomputed for `TreeEdit` | `from` already determines it on every replica. |

## Alternatives Considered

| Alternative | Why not |
|-------------|---------|
| Record the right origin only for siblings at the same boundary | It found smaller divergences in fuzzing and saves little. |
| Compare left origins by ID and treat a purged one as outside the window | It diverged after GC. Skeletons make it unnecessary. |
| Keep physical purge and pin origin targets | Pinned nodes keep their content, and with origins physical purge diverged in 12 of 2,000 Text fuzz runs. |
