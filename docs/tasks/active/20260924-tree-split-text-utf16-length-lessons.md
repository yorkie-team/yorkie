# Lessons: SplitText records the left piece's length in runes

**Created**: 2026-09-24

## What the bug teaches

- `utf16.Decode` returns `[]rune`, so `len()` of its result is a rune count even
  though the slice it came from was indexed in UTF-16 units. The conversion back
  to runes is what silently changes the unit; the two `len()` calls look
  symmetric and are not.
- The split offset is already the length of the left piece. Recomputing a
  quantity you were handed is what let the two disagree.
- Everything downstream — `TreeNodeID.Offset`, `index.Node.VisibleLength`,
  `FindPos` — counts UTF-16 code units because `TreeNode.Length()` does. A unit
  mismatch inside one function stays invisible until a position anchors into the
  affected region, which is why BMP-only text never tripped it.

## Process

- The bug only reaches the server's replay path, not live clients, so the failure
  surfaces as "this document will not load" long after the edit that caused it.
  Tests that assert on a node's recorded length catch it at the source; tests
  that only assert on converged XML do not, since the value is correct — only its
  recorded length is wrong.

## Self review

- Not run. This run is headless and granted no tool that can dispatch a reviewer
  subagent, so no round happened; this is a skipped review, not a clean one. The
  branch's reviewers are CI, `@claude review` and a human.

## Review panel round 2

Two blocking findings, both accepted and fixed:

- **`isolateRange` discarded `splitNode`'s error.** Widening `splitNode`'s guard
  to `offset < 0 || offset > contentLen()` made it return a `nil` node with the
  error, and `isolateRange` was the one caller that threw the error away before
  dereferencing the result. Widening a function's error condition changes every
  caller that was ignoring it — the check to run on such a diff is "who drops
  this error", not just "who reads the new value". `isolateRange`, `restore`,
  `retombstone` and `Text.Restore`/`Text.Retombstone` now return an error, and
  `operations.Edit.Execute` propagates it.
- **Only `TextNodePos.created_at` was nil-checked.** `fromTimeTicket` maps an
  absent ticket to `(nil, nil)`, so hardening one field left every sibling
  operation ticket (`parent_created_at`, `executed_at`, `created_at`,
  `prev_created_at`) able to carry a nil into `Root.FindByCreatedAt` and
  `Ticket.After`. A nil-guard added at one call site of a permissive decoder is
  a decoder-level bug half-fixed: the fix belongs in a required-ticket variant
  (`fromRequiredTimeTicket`) applied across the family.

The second fix surfaced that a locally built reverse operation has no
`executedAt` until `Document.applyUndo` stamps one, so two round-trip tests were
encoding a wire form no real client sends; they now stamp the ticket first.
