---
title: Fix TreeStyle divergence on concurrent split (Root Cause B)
created: 2026-04-10
status: done
relates-to: docs/design/concurrent-merge-split.md
---

# Fix TreeStyle divergence on concurrent split

## Problem

When `TreeStyle` (or `RemoveStyle`) and `SplitElement` (`splitLevel=1`) operate
concurrently on overlapping regions, replicas diverge after synchronization.
This accounts for **6 of the 69** remaining divergences in the property-based
test suite (`test/complex/tree_concurrency_test.go`), all at `splitLevel=1`.

### Failing cases

| Range pattern | Operations | Divergence |
|---|---|---|
| `A_contains_B(split-1, style)` | split "ab\|cd", style "bc" | P gets `bold` on d1, not on d2 |
| `A_contains_B(split-1, remove-style)` | split "ab\|cd", remove-style "bc" | same pattern |
| `right_node(text)(split-1, style)` | split "ab\|cd", style "cd" | P gets `bold` on d1, not on d2 |
| `right_node(text)(split-1, remove-style)` | split "ab\|cd", remove-style "cd" | same pattern |
| `right_node(element)(split-1, style)` | split at element boundary, style second element | mid-p gets `bold` on d1, not on d2 |
| `right_node(element)(split-1, remove-style)` | split at element boundary, remove-style | same pattern |

### Observed divergence (concrete example: `A_contains_B(split-1, style)`)

```
Initial: <root><p><p italic><p italic>abcd</p><p italic>efgh</p></p><p italic>ijkl</p></p></root>

d1: Edit(5, 5, nil, 1)       — split at b|c, splitLevel=1
d2: Style(4, 6, {bold: aa})  — style range covering "bc"

After sync:
  d1: <p bold="aa" italic="true">ab</p><p>cd</p>   ← P got bold
  d2: <p italic="true">ab</p><p>cd</p>              ← P did NOT get bold
```

## Root Cause Analysis

### Why the style range crosses element boundaries after split

1. **Position resolution redirects to split sibling**: When d2's style
   operation resolves its `to` position on d1's (already-split) tree:
   - `to` CRDTTreePos = `(parent=P, leftSibling=text@offset2)`
   - After split, text at offset 2 = text("cd") which now lives in P'
   - `FindTreeNodesWithSplitText` (line 1560-1561) sets
     `realParentNode = leftNode.Index.Parent.Value = P'`
   - Returns `(toParent=P', toLeft=text("cd"))`

2. **Fix 7 advancement is ineffective**: `advancePastUnknownSplitSiblings`
   (lines 1426-1431) cannot help because:
   - `fromLeft = P` and `fromParent = P` → leftmost guard skips advancement
   - `toLeft = text("cd")` → text node, no InsNextID chain to follow

3. **Range crosses P→P' boundary**: `traverseInPosRange(P, P, P', text("cd"))`
   computes indices that span from inside P to inside P'. The traversal
   includes P's End token and P' Start token.

4. **P's End token triggers styling**: The style callback processes P's End
   token → `canStyle(P, ...)` returns `true` (P is known to editor) →
   P gets `bold="aa"`.

5. **On d2 (style before split)**: The range stays within P's text content.
   No element Start/End tokens are encountered. No elements get styled.

6. **Divergence**: d1 styles P, d2 does not → replicas differ.

### Why `collectBetween` (Edit path) doesn't have this issue

`Edit` uses `collectBetween` for traversal, which walks nodes directly
without converting to index positions. It applies `canDelete` per node
with VV checks. When the range crosses into P', `canDelete` prevents
deletion of unknown nodes. No element-level attribute mutation occurs.

### Why `canStyle` is insufficient

`canStyle` already checks `nodeExisted` via the version vector (line 530):
```go
nodeExisted := n.id.CreatedAt.Lamport() <= clientLamportAtChange
```

This correctly prevents styling P' (unknown). But P **is** known to the
editor — its End token just shouldn't be in the traversal range.

## Implemented Fix: End-token split sibling guard (Fix 11)

**Location**: `CRDTTree.Style` and `CRDTTree.RemoveStyle` callbacks,
plus helper `CRDTTree.hasUnknownSplitSibling`.

Initial analysis proposed range clamping, but that approach failed for
`right_node(element)` cases where the editor explicitly selected an
element child that moved to the split sibling. The range must extend
to reach those children.

### Final approach: End-token guard

In the Style/RemoveStyle callbacks, when processing an End token, check
whether the node has a split sibling (via InsNextID) unknown to the
editor. If so, skip styling — the End token is in the traversal range
only because the concurrent split extended it past the original boundary.

```text
if token is End AND node.InsNextID exists AND next sibling NOT in VV:
    skip styling
```

This handles both text-level and element-level ranges:
- **Text-level** (A_contains_B, right_node(text)): P's End token skipped,
  P' Start rejected by canStyle → no elements styled
- **Element-level** (right_node(element)): mid-p End token skipped,
  inner-p-efgh Start token styled normally → correct element styled

### Result

Fixed **16 divergences** (not just 6): all `splitLevel=1 × style` cases
plus `splitLevel=2 × style` cases that shared the same mechanism.

| Before | After |
|---|---|
| 1528 pass, 69 skip | 1545 pass, 53 skip |
| 2 root causes | 1 root cause (splitLevel≥2 only) |

### Files modified

| File | Change |
|---|---|
| `pkg/document/crdt/tree.go` | Added `hasUnknownSplitSibling` helper; End-token guard in `Style()` and `RemoveStyle()` callbacks |
| `docs/design/concurrent-merge-split.md` | Added Fix 11 section; updated Style table, summary, remaining issues, design decisions |
