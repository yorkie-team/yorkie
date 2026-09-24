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

## Review panel round 3

Same shape as round 2's second finding, one level down. Hardening had been
applied to the *operation's* tickets but not to the *element* those operations
carry, so a Set/Add/ArraySet payload with no `created_at` still reached
`ElementRHT`/`RGATreeList`, which key on `v.CreatedAt().Key()`. Three lenses
independently found it, plus the `TreeEdit.split_tickets` entries, still decoded
through the permissive `fromTimeTicket`. The lesson repeats: when the defect is
"a permissive decoder is trusted at a dereference site", the fix has to sweep
every site that decoder feeds, not the family you happened to be looking at.

The other finding was the counterpart the repo already documents: every
rejection added to `FromOperations` also lands on `ChangeInfo.ToChange`, which
reads changes written under the older rules, and there a rejection makes the
document permanently unloadable. Two of the new rejections have no repair — you
cannot invent a ticket, and substituting one only moves the failure from decode
to execute — so the stored path now *drops* those operations
(`FromStoredOperations`). That is defensible precisely because such an operation
could never have applied anywhere: the nil ticket faulted inside `Execute`, so
no document ever held its effect. The split-ticket list is repairable and is
truncated at the first absent entry instead, since `TreeEdit.Execute` already
falls back to reconstructing the tickets it runs out of.

Worth recording: `withoutUndatedOperations` asks `fromOperation` rather than
re-listing the required fields. A hand-written mirror of "what is required"
drifts from the wire boundary on the next round; asking the decoder cannot.
And it runs only after the ordinary decode has reported `ErrMissingTicket`, so
the stored path pays nothing in the normal case.
