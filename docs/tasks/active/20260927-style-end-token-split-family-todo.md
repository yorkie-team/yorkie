# A level-2 split before a style range styles the paragraph it split

**Created**: 2026-09-27

Counterpart of yorkie-team/yorkie-js-sdk#1404, which ports #2038 to the JS
SDK and found this while doing it.

## Problem

The complex suite's `concurrently-split-edit-test`, range `A -> B`, splits
`<p>abcd</p>` at level 2 while the other client styles (or removes a style
from) `<p>efgh</p>`, the paragraph right after it:

```
<r><p><p><p italic>abcd</p><p italic>efgh</p></p><p italic>ijkl</p></p></r>
A: Edit(5, 5, nil, 2)          // split <p>ab|cd</p> two levels up
B: Style(8, 14, {bold: "aa"})  // bold <p>efgh</p>
```

Since #2038 the two delivery orders disagree: one styles `ab`, `cd` and
`efgh`, the other only `efgh`. At `ac88215d^` both orders agreed. The complex
suite reports a non-converging pair with `t.Skip`, and its lane runs only
behind a path filter, so nothing failed. The #2038 scans split one level of a
flat tree and never produce this shape.

## Mechanism

A level-2 split carries the right half `cd` into a NEW parent. The style's
range began right after `<p>abcd</p>`; `advancePastUnknownSplitSiblings`
stops at a parent change, so the traversal now passes the right half's End
token. The split-family branch of `styleTargets` reads an End token as "the
range ran past this element's end" and styles the whole family. An element is
reached through its End token alone only when the range began inside it
(§9.6), which this range did not.

## Plan

- [x] Red: document-level two-replica replay of the pair, Style and
      RemoveStyle, in `pkg/document/tree_style_reached_set_test.go`
- [x] Confirm `ac88215d^` converges on the same pair
- [x] Fix: the split-family branch also requires
      `beginsInside(family[0], declaredFromParent)` — the JS guard verbatim
- [x] Green; existing scan counts unchanged (1001/759/1862, 7098/4254/6302,
      tombstone-only ≤ 1292)
- [x] Extend the scans to a nested base with split levels 1 and 2, so this
      class is caught in the unit lane; record before/after counts
- [x] Design doc: state the rule in §9.2 and the Port specification
- [x] `make verify`, `make test` if MongoDB is up, complex split-edit subset
- [x] Self review (max 3 rounds), log in lessons
- [x] Rebase, push, open PR linking yorkie-js-sdk#1404

## Result

Red on main, both Style and RemoveStyle: split-first styles (or clears)
`ab`, `cd` and `efgh`, style-first only `efgh`. The same test passes at
`ac88215d^`. With the guard, both orders give the style-first answer.

| scan | pairs | before #2038 | main | this change |
|---|---|---|---|---|
| split x style (flat) | 1001 | 135 | 0 | 0 (759 / 1862 styled) |
| merge x style (flat) | 7098 | 297 | 0 | 0 (4254 / 6302, tombstone 1292) |
| nested split x style | 11592 | 2810 | 363 | 241 (9318 / 33208) |

RemoveStyle matches Style on every count. Per pair: the guard turns no
converging pair into a diverging one; it closes 36 pairs #2038 broke and 86
older ones. Every complex-suite split-edit pair now converges (145 pass, 0
skipped).

## Known limitation

152 nested pairs that converged before #2038 still diverge: a split of the
element the range end is declared in, before that position. The split-first
order styles the original half, which the range never reached. That is a
boundary-element rule (§9.5), not this one; it needs a matching JS change and
is recorded in the design doc's known limitations.
