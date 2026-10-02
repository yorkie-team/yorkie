# Reject mid-surrogate-pair indexes at the local API

**Created**: 2026-10-02

## Problem

An edit at an index that falls between the two UTF-16 code units of a
surrogate pair splits a text node mid-pair. Go and JS then hold different
content for the same operation:

- Go turns each lone half into U+FFFD, because `utf16.Decode` on a slice
  that starts or ends inside a pair cannot do anything else.
- JS keeps the lone halves `\uD83D` / `\uDE00`.

Since #2035 node IDs and lengths agree (both count UTF-16 code units), so the
tree *structure* converges. The *text* does not: a client that received the
change directly and a client that loaded the server snapshot see different
strings.

Repro: `<r><p>😀x</p></r>`, then `Edit(2, 2, "y")` →
`{"value":"�"},{"value":"y"},{"value":"�x"}`.
`Text.Edit` has the same problem through `TextValue.Split`.

## Decision

Issue #2065 option 1: reject mid-pair indexes at the local API, in both SDKs.
This repo is the Go half.

A local index that is not mid-pair maps to a node offset that is not mid-pair
on every replica, because node contents are identical everywhere. So no new
operation can carry a mid-pair offset and the CRDT split rule is untouched —
no wire-level change, no rollout ordering, old clients keep today's behavior.

Option 2 (align the split forward) was rejected upstream: it changes node IDs
for the same operation, making it a convergence rule that needs server-first
rollout.

## Scope

In scope — the local, index-addressed entry points in `pkg/document/json`:

- `Tree.Edit`, `Tree.EditBulk`, `Tree.Style`, `Tree.RemoveStyle`
- `Tree.EditByPath`, `Tree.EditBulkByPath`, `Tree.StyleByPath`,
  `Tree.RemoveStyleByPath` (a path's last component is an offset into a text
  node, so it reaches the same split)
- `Text.Edit`, `Text.Style`

Out of scope:

- Remote operation application. `crdt.Tree.FindPos` /
  `RGATreeSplit.createRange` are shared with the remote and undo paths; a
  mid-pair offset arriving from an old client keeps today's behavior, exactly
  as the issue specifies.
- The JS SDK half (yorkie-js-sdk).
- Any change to `SplitText` / `TextValue.Split` themselves.

## Plan

1. `pkg/document/crdt/surrogate.go`: `IsMidSurrogate(value string, offset int)
   bool` — true iff the code unit before `offset` is a high surrogate and the
   one at `offset` is a low surrogate.
2. `crdt.Tree.IsMidSurrogate(idx int) (bool, error)` — resolve the index to a
   text node + relative offset via `IndexTree.FindTreePos`, then ask (1).
   Non-text positions are never mid-pair.
3. `crdt.Tree.IsMidSurrogateAtPath(path []int) (bool, error)` — same via
   `IndexTree.PathToIndex`.
4. `crdt.Text.IsMidSurrogate(idx int) (bool, error)` — resolve via
   `treeByIndex.FindForText`, then ask (1).
5. `json.ErrMidSurrogatePair` (`errors.InvalidArgument`), panicked from the
   entry points above, matching how those functions already report caller
   errors.
6. Unit tests: `pkg/document/json` (or `pkg/document` doc tests) covering the
   issue's repro for Tree and the equivalent for Text, plus the boundaries
   that must still be accepted (0, the far side of the pair, end of value)
   and a BMP string that must be unaffected.

## Acceptance criteria

- `Tree.Edit(2, 2, text "y")` on `<r><p>😀x</p></r>` is rejected instead of
  producing `�y�x`.
- `Text.Edit(2, 2, "y")` on `😀x` is rejected.
- Indexes 0, 1 (before the pair), 3 (after the pair) and 4 (end) stay valid.
- Styles over a mid-pair boundary are rejected on both Tree and Text.
- Pure-BMP documents are unaffected; the existing suites stay green.
