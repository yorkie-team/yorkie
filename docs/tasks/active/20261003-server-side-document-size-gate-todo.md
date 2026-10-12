**Created**: 2026-10-03

# Server-side gate for MaxSizePerDocument

## Problem

`MaxSizePerDocument` is a per-project quota (default 10 MiB) that only the
client enforced. The server sends it in the attach response, the SDK stores it
on the document, and `Document.Update` refuses the local update when the
clone's `DocSize.Total()` exceeds it. The push path never re-read it, so a
modified SDK or a direct Connect call could grow a document past the quota,
bounded only by `maxRequestBytes` per push and, much later, MongoDB's 16 MiB
record limit. `docs/design/document-size-limit.md` recorded the candidate
gates; this task picked one and built it.

## Plan

- [x] Decide the refusal semantics: refuse growth only. When the recorded live
      size is strictly above the quota, a pack with any change that can grow
      the document is refused; removals, content-free `Edit`/`TreeEdit` and
      presence-only changes are admitted. Detach and remove always go
      through, with growth changes in them discarded. Written back into
      `docs/design/document-size-limit.md`.
- [x] Decide where the number comes from: `live_size` on the snapshot row,
      written by `CreateSnapshotInfo` in both drivers and read by
      `checkDocumentSize` (`server/packs/size_gate.go`) with
      `FindClosestSnapshotInfo(..., false)`. Not on `DocInfo`: the reverted
      first attempt showed a field on the cached `DocInfo` races the
      `docCache`.
- [x] Compaction resets the size by purging snapshots; zero means unknown and
      admits. Covered by `RunSnapshotLiveSizeTest` (memory and mongo).
- [x] Error code: reuse `document.ErrDocumentSizeExceedsLimit`, now with code
      `ErrDocumentSizeExceedsLimit` (`ResourceExhausted`), returned as a
      bare `StatusError` so `connecthelper` attaches the code.
- [x] Integration test over raw Connect calls (`TestDocumentSizeGate`): growth
      past the quota is refused with that code and the server seq does not
      move; a remove-only pack on the over-quota document is admitted and
      re-opens growth once a snapshot measures it; a detach carrying refused
      changes goes through without them.
- [x] Document the overshoot (up to one `SnapshotInterval` of changes, each
      push capped by `maxRequestBytes`) as part of the quota's contract.
- [ ] yorkie-js-sdk follow-up (separate PR in that repo): treat
      `ErrDocumentSizeExceedsLimit` from `PushPullChanges` as terminal for
      the document instead of retrying the same pack — surface an over-quota
      state, stop the sync loop for it, and let the app detach. The code
      already exists in `Code` (`src/util/error.ts`) for the local check, so
      only the push-path handling is new. The Go client needs the same.

## Out of scope

- The client-side check. It stays: it is what gives an honest client a
  synchronous error at the edit that exceeds the quota.
- Byte-exact agreement between the server's number and the client's. Making
  the running `DocSize` accumulator agree with a rebuild is tracked by the
  rebuild-drift task.
- Counting bytes pushed since the last snapshot to tighten the overshoot.
