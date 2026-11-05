# Skip the presence clears of a server-side detach on presenceless documents — Lessons

## What changed

`clusterServer.DetachDocument` reads `DocInfo` first. On a document created
with `disable_presence` it builds no presence clears and so never calls
`hasAttachedSibling`. Everything else about the detach is unchanged.

## Why it is behaviour-preserving

- The clears never reached the document before either: `PushPull` strips
  presence-only changes on presenceless documents before `pushPack`
  (`server/packs/pushpull.go`, `strip.go`), so `pushPack` saw an empty
  `Changes` slice in both versions.
- The recorded checkpoint is the same: with nothing pushed,
  `CreateChangeInfos` returns the request checkpoint, so the stripped clears
  never advanced `clientSeq`.
- `RemoveOnDetach`, the attachment-limit lock, webhooks and the doc-changed
  publish take the same branches, since they depend on `pushedChanges` and
  `IsRemoved`, not on the clears.
- The test checks the document's `ServerSeq` before and after the detach;
  it is unchanged on presenceless documents in both versions.

## Measured

Local MongoDB, one document with 88,386 activated attached clients, a
synchronous `DeactivateClient` that makes the server detach:

| | before | after |
|---|---|---|
| presenceless document | 659 ms, 1 read of all attached clients | 7 ms, 0 reads |
| document with presence (out of scope) | 710 ms, 1 read | 678 ms, 1 read |

Production pays more per read (38.5 MB over the network, ~1.4 s end to end),
so the gap there is larger.

## Self review

### Round 1 — correctness and tests (independent reviewer)

No blocking findings. Non-blocking, handled:

- The read count was absolute; an attach on a project with an attachment
  limit, `RemoveOnDetach` or a schema also reads the attached clients. The
  test now measures the detach alone (delta around `DeactivateClient`).
- "The detach is unchanged" was only checked through the client row. The
  test now also checks the document's `ServerSeq`: unchanged on a
  presenceless document, increased on a document with presence.
- Wrapping `be.DB` in the probe defeats the `*mongo.Client` assertion in
  `clusterServer.InvalidateCache`, so cluster cache invalidation is a no-op in
  the `server/rpc` integration tests. Harmless on one node; noted in
  `TestMain` so a future invalidation test there does not pass vacuously.

Non-blocking, left as is:

- `FindDocInfoByRefKey` now runs before the document locks. `DisablePresence`
  and `Key` never change after creation, and the same unlocked read already
  exists on other paths (Watch, revisions), so this adds no new kind of race.
  It also means a key mismatch or a missing document now fails before the
  sibling check instead of after it.
- On presenceless documents an invalid `StableActorID` or a failing sibling
  read no longer fails the detach. Those failures were for clears that would
  have been dropped.
- Outside this diff: `docs/design/disable-presence.md` says a stripped
  presence-only change still has its `clientSeq` acknowledged; the code does
  not advance it. Worth a separate fix.

Stopped after round 1, the first round without blocking findings.

## Known limitations

- Documents with presence still read every attached client on each
  server-side detach. Narrowing that check (an existence query on
  `stable_actor_id`, with an index) belongs with #2294.
