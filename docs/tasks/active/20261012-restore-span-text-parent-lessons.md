# Lessons: restore span with a text parent

## Red evidence

On unmodified `main` (180780d9), both new tests fail with the error the
issue describes, for an element span and for a text span alike:

```text
--- FAIL: TestRestoreSpanUnderTextParentStaysApplicable/element_span
    tree_restore_text_parent_test.go:141: Received unexpected error:
        text node cannot have children
--- FAIL: TestRestoreSpanUnderTextParentStaysApplicable/text_span
    tree_restore_text_parent_test.go:141: Received unexpected error:
        text node cannot have children
--- FAIL: TestTreeRestoreTextParentSkip
    tree_restore_test.go:263: Received unexpected error:
        text node cannot have children  (isText=false, then isText=true)
```

Line 141 is the pulling replica's `ApplyChangePack`. The crafted pack
decodes without error, which confirms decode cannot catch it.

## Notes

- Decode-time validation is the wrong layer here. The converter sees a
  span, not the document, so it cannot know the parent ID names a text
  node. A push-time check would also leave changes already stored in the
  database unloadable.
- The skip is convergent because node kind is fixed at creation. A parent
  ID resolves to the same text node on every replica that still holds it,
  and to nothing on a replica that purged it, which B1 already skips.
- The text-span variant reaches the same `InsertAt` fallback: a text
  parent has no children, so rungs (a) to (c) all miss.

## Self-review log

### Round 1: external code review of 7fcc0733 (verdict: approve)

1. **IsText disagreeing with NodeType was unchecked.** The reviewer probed
   two shapes. `IsText=false, NodeType="text"` recreated a zero-length text
   node counted as recreated. `IsText=true, NodeType="p"` recreated an
   element carrying a Value; the text lookup cannot see an element, so a
   second copy of the span inserted another node under the same ID
   (`<r><p>hello<p></p><p></p></p></r>`), the duplicate-TreeNodeID shape
   that has panicked snapshot rebuilds before.
   - Red on 7fcc0733: `TestTreeRestoreKindMismatchSkip` (recreated non-empty
     for both shapes, the duplicated `<p>` in the XML) and the
     `text span typed as an element, twice` subtest of
     `TestRestoreSpanUnderTextParentStaysApplicable` (same XML on a pulling
     replica and the rebuild).
   - Resolved at apply time, not decode. `FromStoredOperations` and the
     pull path run the same strict `FromOperations` decoder as a push, so a
     decode rejection would make any document already holding such a span
     unloadable, and every client pull of it would fail too. `Restore` and
     `Retombstone` now skip a span whose kind disagrees. The check reads
     only the span, so every replica skips it alike. Honest spans always
     agree: both SDKs set `isText` from `type == "text"`.
2. **"Mirrors the JS CRDTTree.recreateFromSpan" was not true yet.** Reworded
   to say JS must apply the same rule (yorkie-js-sdk#1471).
3. **No self-review log.** This section.

Lesson: when hardening a wire payload, list every field the apply path
trusts, not only the one the issue names. A span carries its kind twice.

## Review round 3 (panel)

1. **The span's own identity was still untrusted.** The parent and kind
   checks cover what the span says about OTHER nodes and about itself, but
   nothing tied `span.ID` to a node of its own insertion. A text span whose
   `CreatedAt` names a live ELEMENT slips past `findPiecesOverlapping`
   (which stops at a non-text floor node), reaches the gap branch, and
   `putNode` then overwrites the element's `NodeMapByID` entry — the same
   "change cannot be applied / document cannot be rebuilt" failure as
   #2168. An element span whose offset falls inside a live text run plants
   the ID the next honest split of that run mints. `recreateFromSpan` now
   skips a span whose identity a surviving node already holds, exactly or
   inside a surviving run of the same insertion. Both of `Restore`'s
   branches already reach the recreate only for unheld identities, so an
   honest span never trips it.
   - Red before the fix: both subtests of
     `TestTreeRestoreClaimedIdentitySkip`.
2. **A guard with a vacuous test is an untested guard.** The `Retombstone`
   half of `TestTreeRestoreKindMismatchSkip` used spans carrying fresh
   tickets, so no node matched and the assertions held with or without the
   guard. `TestTreeRetombstoneKindMismatchSkip` now names a live node, and
   `TestTreeRestoreKindMismatchKeepsTombstone` covers the un-tombstone
   branch the early `continue` bypasses. Every new test was run against a
   build with the guards stripped out to confirm it goes red.
3. **The kind check keys on `node_type`, a free-form wire string** no
   decoder or proto rule constrains. Tightening that belongs in
   `api/converter/from_pb.go`, outside this change's files.

Lesson: a guard is only worth the test that fails without it — strip the
guard and re-run before believing the test covers it.
