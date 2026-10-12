**Created**: 2026-10-10

# Stop the sync loop retrying a push the server will never accept

The Go half of yorkie-js-sdk #1462.

## Problem

The server refuses a push that would grow a document past the project's
`MaxSizePerDocument` (`ErrDocumentSizeExceedsLimit`) or that does not fit in
one record (`ErrChangeTooLarge`). Nothing the client resends changes that
answer. The Go client's realtime sync loop (`runSyncLoop`) still treats it
like a network blip:

- `needSync` stays true while the document has local changes, so the same
  pack goes out again on every round, and since push and pull share one
  `PushPull`, the document also stops receiving its peers' changes.
- After every failure the loop sleeps `RetrySyncLoopDelay` (1s) *inside*
  the walk over attachments, so one refused document holds up every other
  document and channel on the client for a second, each round, for as long
  as the refusal lasts.

The local size check in `Document.Update` does not prevent this: it only
knows the limit read at attach time and the client's own view of the
document, and the server measures what peers wrote too.

## Plan

1. `Attachment.retryAt` — the time before which the loop leaves this
   attachment alone. `needSync` returns false until then; every other
   attachment keeps syncing meanwhile.
2. `Attachment.recordSync`, called by `syncInternal` while it still holds
   `syncMu` — on `ErrDocumentSizeExceedsLimit` or `ErrChangeTooLarge` from
   the loop's own sync, hold the document off for the longer
   `RejectedPushRetryDelay` (10s). On any other loop failure, hold that
   attachment off for `RetrySyncLoopDelay` instead of `runSyncLoop`
   sleeping the whole loop.
3. A successful sync clears it, so an explicit `Client.Sync` that goes
   through puts the document back in the loop at once. An explicit `Sync`
   still returns the refusal to the caller, as today, and holds nothing off.

Why a delay and not a park until an explicit `Sync`: the refusal is the
server's current answer, not a permanent one. The size gate reads the
project's limit and the document's latest snapshot size, and both can move
without this client — an operator raises the limit, or peers delete content.
An app driven by the watch stream alone never calls `Sync`, so a park would
leave it desynced for good, and silently: push and pull share one `PushPull`,
so a document waiting out a refusal pulls nothing either. The delay bounds
that staleness, and costs one refused push per document per 10s.

What this does not fix: the refused change stays in the local queue, and the
server refuses a pack that holds any growing change, so deleting content on
this client does not get the document moving again. It syncs again only when
the server would accept the pack. Until then the document is stuck, as it was
before; the difference is that it no longer floods the server, no longer
stalls the rest of the client, and recovers by itself when the server's
answer changes.

Out of scope: recovering a document whose writes the auth webhook denies
(the JS #1463 resync), which needs an API Go does not have.

## Checklist

- [x] Client test against a fake server (Red first): a refused document is
      pushed once, not every round; another document's edit still goes out
      within a round; an explicit `Sync` that succeeds resumes the loop
- [x] Implementation (Green)
- [x] `make verify`, `make test` (on the final tree, run untouched)
- [x] Self review
- [ ] PR

## Review

- Red on main: `TestSyncLoopParksRejectedPush` saw the refused pack pushed
  3 times in 2.5s, and another document's edit waited 505ms for the 1s
  retry sleep. `TestSyncLoopRetriesFailedSyncAlone` (added in round 1) saw
  another document wait the full 1.0s behind an `Unavailable` one. Both
  green after the change, 3/3 under `-race`.
- Self review round 1 (correctness/tests, independent agent): nothing
  blocking. Fixed: the park flag was stored after `syncMu` was released, so
  a concurrent explicit Sync that succeeded could be overwritten (now
  recorded under the lock); `retryAfter` used the wall clock (now
  monotonic); the design doc implied a parked document unparks by itself;
  `ErrChangeTooLarge` is only returned by compaction today (comment says
  so); no test covered the generic-failure path (added).
  Known, for the PR body: during a server outage each attachment with
  something to sync now retries once per `RetrySyncLoopDelay` on its own,
  where the old sleep serialised them; a parked document is only logged,
  with no event an app can react to (JS has `write-rejected`).
- Self review round 2 (design fit/docs, independent agent): nothing
  blocking. Fixed: `document-size-limit.md` still said the SDK retries the
  pack, and the `RetrySyncLoopDelay` comments still described the loop-wide
  sleep; the new fields were atomics plus a package-level clock anchor
  although both are only touched under `syncMu`, so they are now plain
  fields like `lastSyncTime`, with `time.Time` carrying the monotonic
  reading; the `opts == nil` means-the-loop contract is now stated on
  `syncInternal`.
- Self review round 3 (blast radius/correctness/tests): the indefinite park
  was the blocking one. A document parked until an explicit `Sync` never
  pulls again either, so an app that only watches keeps being told about
  remote changes it will never see, with no signal and no way back — and
  the server's refusal is not permanent to begin with. The flag is gone:
  a refused push now takes the same `retryAt` hold-off as any other
  failure, with the longer `RejectedPushRetryDelay`, so the loop re-probes
  by itself. Tests added for the paths the end-to-end ones could not
  reach: the re-probe, a failed explicit `Sync` leaving the attachment
  alone, a held-off document not pulling a pending remote change, and a
  channel not taking the document delay.
  Known, for the PR body: a document whose push keeps being refused is
  still up to `RejectedPushRetryDelay` behind its peers, because the Go
  client has no pull that leaves the refused pack behind — that needs a
  pull-only request the wire protocol does not have today.
