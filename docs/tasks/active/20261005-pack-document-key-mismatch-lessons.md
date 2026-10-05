**Created**: 2026-10-05

# Reject Change Packs Naming Another Document — Lessons

Plan: `20261005-pack-document-key-mismatch-todo.md`.

## Where the check goes

All three handlers write only in `packs.PushPull`, after
`documents.FindDocInfoByRefKey`, and the lookup is already there to read
`disable_presence`. Comparing right after it costs nothing extra and stops
the request before any write. Earlier steps only read (client lookup,
`IsDocumentAttachedOrAttaching`) or take locks; the locks were taken on the
pack's key, which is the right key once the check passes.

Checking before `VerifyAccess` would need the lookup before authorization;
nothing gains from that, since the webhook answer is discarded on mismatch.

`AttachDocument` has no `DocumentId`; it finds or creates the document by the
pack's key, so it has nothing to compare. The revision handlers already build
their webhook attributes from `docInfo.Key`.

## Proving the test

Without the check, each subtest fails: PushPull and Detach apply the pack's
`{"x":1}` to the target, and Remove removes it. The first version shared one
target across the subtests, and without the check the later requests hid the
earlier one's write (the final `{}` passed by accident), so each subtest now
uses its own pair of documents and checks its own target.

## Self-review (round 1, correctness/tests)

- The SDKs always send the attached document's own key in the pack, so a
  legitimate client never hits the new error.
- `docInfo` comes from the LRU cache by ref key; a document's key never
  changes, so a cached entry is as good as a fresh read.

No blocking findings.
