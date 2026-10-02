**Created**: 2026-10-02

# Take the reply to a push-only request as a push ack only

**Port of:** yorkie-team/yorkie-js-sdk#1393 (SDK part, `client.ts`).

## Problem

The server attaches the minimum version vector to every PushPull reply,
including the reply to a push-only request (`server/packs/pushpull.go`, the
`UpdateMinVersionVector` branch runs regardless of mode). The Go client hands
that reply to `Document.ApplyChangePack`, whose step 04 garbage-collects with
it.

A push-only client pushes without pulling, so the vector can cover a removal
whose concurrent remote changes the client has not pulled yet. Collecting the
tombstone they anchor on leaves the first full pull after the pause unable to
apply them (`cannot find node`), and the server redelivers the same pack on
every later sync.

## Approach

- [x] `Document.AcknowledgePushedChanges(pack)`: drop the local changes up to
      the pack's client seq, forward the checkpoint's client seq only (server
      seq stays so a later pull fetches whatever was skipped), and take the
      removal flag. No changes, no snapshot, no GC.
- [x] `Client.pushPullChanges`: when the request was sent push-only, call
      `AcknowledgePushedChanges` instead of `ApplyChangePack`. Judged by the
      mode the request was sent in (`opt.mode`), as JS does.
- [x] Integration test mirroring `pushonly_gc_test.ts`: a tombstone a deferred
      remote change anchors on survives the push-only reply, and both
      replicas converge after the full pull.
- [x] Note the push-only case in `docs/design/garbage-collection.md`.
- [x] `make verify`, `make test` (gc / client suites).

## Out of scope

- JS also drops remote state from a full PushPull while the attachment is in
  `RealtimePushOnly`/`RealtimeSyncOff` (an explicit `sync(doc)` during an IME
  composition). The Go client applies such a pull in full, which is safe: it
  pulls every change up to the vector before collecting with it.
- JS's `acknowledgePushedChanges` also takes the pack's epoch. The Go
  `Document` does not track an epoch, so there is nothing to take.
