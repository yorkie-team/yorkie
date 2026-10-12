**Created**: 2026-10-12

# Resolve the index before a text that follows an element

Fixes yorkie#2078. The JS half is yorkie-js-sdk#1481.

## Problem

`toTreePos` anchored a text reached at offset 0 on the text itself
(`LeftSiblingID = {text, 0}`) unless the text was the first child. A left
sibling means "after it", so in `<p><b>hello</b> world</p>` index 8
inserted after ` world`, and `Edit(8, 14)` deleted nothing.

## Fix

A text at offset 0 anchors on the parent when it is the first visible
child, otherwise on the previous visible sibling. That sibling is always an
element: `FindTreePos` resolves a boundary between two texts to the end of
the left one.

- Only local index conversion changes. Remote replicas apply the position
  as-is, so convergence and the wire format are unchanged.
- The rule is recorded in `docs/design/tree.md` (Tree Coordinate System).

## Cross-SDK parity

The position computed here is what goes on the wire, so the JS SDK must
resolve the same index the same way. `CRDTTreePos.fromTreePos` still has the
old first-child-only rule; yorkie-js-sdk#1481 tracks the mirror change. Until
it lands, a JS client at this boundary keeps the misplaced edit of #2078;
replicas still converge because each applies the position it receives.

## Checklist

- [x] Anchor a text at offset 0 on the previous visible sibling or parent
- [x] crdt-level tests: insert, delete, tombstones, split element, split
- [x] Two-replica tests under a concurrent split (insert, delete, split,
      style)
- [x] Record the anchoring rule and the parity requirement in tree.md
- [x] File the JS half (yorkie-js-sdk#1481)
- [ ] JS mirror change lands
