**Created**: 2026-10-05

# Auth Webhook: Presence-only Packs Report `r` — Lessons

Plan: `20261005-auth-webhook-presence-only-read-todo.md`. Design:
`docs/design/auth-webhook-verb.md`.

## Why option A after building option B

Option B (`hasOperations` next to an unchanged `verb`) was built first, for
backward compatibility. Weighing what A actually takes away changed the call:
A only removes the ability to gate presence as a write, and no webhook could
use that ability. Attach and detach always carry presence, so a rule that
rejects presence from a member already stops it from opening the document,
the same as rejecting the method. B kept every existing webhook as broken as
before until it learned a new field, and it needed a `*bool` (to tell "no
operations" from "not a pack" or "older server"), which in turn leaked into
`watchStream.decisionKey`'s `%v` formatting. A is one condition in
`auth.AccessAttributes`.

## Removal was reported as `r`

Building the new rule on operations alone would have kept a hole that exists
on main today. `RemoveDocument` sends a pack with `IsRemoved` and usually no
change (the Go and JS SDKs both set only the flag), so `HasChanges()` was
false and a removal reported `r`. A webhook that rejects `rw` from readers
let them remove documents. The rule is now `OperationsLen() > 0 ||
IsRemoved`. The integration subtest "a reader cannot remove the document"
fails without the `IsRemoved` term: the removal succeeds and the following
detach errors because the document is gone.

## Proving each subtest catches its case

- Old rule (`HasChanges()`): the attach/detach subtest fails (the reader is
  rejected), and so does the removal subtest, but only because its attach is
  rejected first. That does not prove the removal case.
- Operations only, without `IsRemoved`: only the removal subtest fails, now
  for the right reason.

## Self-review (round 1, correctness/tests)

An independent reviewer read the diff against the four handlers. Findings:

1. **Blocking, fixed: schema binding on attach went through as `r`.** With no
   client attached and a schema named, `AttachDocument` binds it
   (`UpdateDocInfoSchema`). The pack is presence only, so a read-only member
   could pick the schema later edits are checked against. Whether the attach
   binds is known only after the document lookup, which runs after the first
   check, so the handler now asks the webhook again with `rw` right before
   binding when the first check was a read. Reporting `rw` for any named
   schema instead would block readers from every schema document. Subtest "a
   reader cannot bind a schema but can attach under one" fails without the
   recheck (the bind succeeds).
2. **Blocking as a doc mismatch, documented: `RemoveOnDetach`.** The handler
   sets `IsRemoved` itself after the webhook check, so the last client's
   detach removes the document while reported as `r`. Kept: the removal is
   the project's policy, and rejecting the detach would keep a reader from
   ever leaving. The design doc's table now says the `rw` row is for removals
   the client requests, and a "What `r` still allows" section lists this and
   the empty document an attach creates for a new key.
3. **Non-blocking, fixed:** the design doc claimed the security guide already
   says `r` may carry presence. It does not yet; reworded as a follow-up.

Checked without findings: `Snapshot`/`VersionVector` in the request are not
written as content; nothing else depends on the old verb
(`HasChanges()` in `server/packs/pushpull.go` is unrelated); the Go client
always sends presence on attach and detach, so the subtests are meaningful;
each subtest uses its own key and the cache key includes the verb.

Noticed, out of scope: inside the `count == 0` branch, projects with an
attachment limit or `RemoveOnDetach` rebind the schema to whatever the
request names, including an empty key, even when the document already has
one. With this change a reader's attach there also needs `rw`.

## Self-review (round 2, design fit)

No blocking findings in the diff; the rounds stop here.

- The recheck runs under the same locks and leaves the same state on error as
  the `GetSchema` error just above it. The leftover attaching entry is not
  counted by `FindAttachedClientCount`, so it does not block a later bind.
- It uses `VerifyAccess` with `NewAccessAttributes`, the shape the channel
  handlers use, and the verb keeps the `r` and `rw` answers apart in the
  cache.
- Non-blocking, documented in the design doc: the recheck also fires for an
  empty schema key on attachment-limit / `RemoveOnDetach` projects (the
  rebinding noted above); the second webhook call holds the attachment lock;
  creating a key also fixes its `disable_presence`.

## For the PR body

- Release note: a presence-only pack now reports `verb: "r"`, and a removal
  reports `"rw"` even without changes.
- The security guide (yorkie-team.github.io#344) currently describes v0.7.23,
  where `rw` means "any change, presence included". It needs updating once
  this ships.
