# Bound the presence a client may push as a put

**Created**: 2026-11-19

Follow-up to the review of #2384, filed as #2393. The code is outside that
PR's diff and the finding is independent of it.

## Problem

`maxPresenceBaseSize` (4 KiB) bounds a cache entry and the result of a fold;
`maxPresenceFoldPerPack` (64 KiB) bounds the sum of a pack's *folded* puts. A
`PUT` the client sends itself is deliberately exempt from both
(`presence_patch.go`, the `presence.Put` arm sets `base, coalesced =
pc.Presence, -1`), on the reasoning that it is "paid for in the bytes the
client sent".

That reasoning covers the write, not the fan-out. Nothing on the push path
measures a client-sent put: `converter.FromPresenceChange`
(`api/converter/from_pb.go`) applies no size check, and `checkDocumentSize`
measures operations only — `canGrow` returns false for a presence-only change
(`server/packs/size_gate.go`), so presence is excluded from every document
size gate. The change is stored through `CreateChangeInfos` and then pulled by
every attached client. One authenticated push of a single put just under the
16 MiB `WithReadMaxBytes` the RPC server reads is stored verbatim and replayed
to N peers: N× amplification with N bounded by nothing on this path.

## Fix

Refuse a push carrying a presence `PUT` larger than `maxPresencePutSize`,
measured in the same `presenceSize` the other two bounds use, with
`ErrPresenceTooLarge` (`ResourceExhausted`). Only the changes `pushPack` will
store are measured — the ones above the client's checkpoint — so a resend that
repeats an already-acked change is not refused for a presence the server
accepted before this bound existed.

Budget: 64 KiB, the same 16 × `maxPresenceBaseSize` the pack fold budget uses.
It cannot be `maxPresenceBaseSize` itself: a presence over 4 KiB is a
supported, if degraded, case today — such a client is never folded into nor
cached and pushes full puts instead (the motivating app of #2154 held about
7.4 KB) — so refusing at 4 KiB would break clients that work now. 64 KiB is
well above any presence a cursor or a selection needs and bounds the per-peer
fan-out of one push to a constant.

The push is refused rather than coalesced. Unlike passing the fold budget,
which is a property of the pack's shape and would recur on every resend, an
oversized put is a property of the presence the client chose to send: the
client clears it by sending a smaller presence, and the error names a limit,
so a resend of the same pack is correctly refused again. `ResourceExhausted`
puts it with `ErrDocumentSizeExceedsLimit` and `ErrChangeTooLarge`, the other
"resending this will not help" rejections.

## Plan

- [x] `maxPresencePutSize` and `ErrPresenceTooLarge` in `presence_patch.go`,
      with `checkPresenceSizes` walking the pushable changes.
- [x] Call it from `PushPull` after the presence strip and before the fold.
- [x] `client.isWriteRejected` recognises the code, so the Go client's sync
      loop holds the document off instead of retrying a push that cannot
      succeed.
- [x] Internal tests: a put at the bound passes; one over it is refused with
      the code and status the client matches on; a put of many tiny keys is
      refused; a put at or below the checkpoint, a patch, a clear and a
      change without presence are not measured.
- [x] An integration test driving an oversized put through `AttachDocument`,
      so the refusal is checked against the real RPC surface.
- [x] `docs/design/presence-patch.md`: a "Put size" section, the risk row and
      the decision row.

## Out of scope

- A configurable per-project presence quota. The bound is a constant like the
  other two; making presence a project-level quota is a separate decision
  about what `MaxSizePerDocument` measures.
- Enforcing in `converter.FromPresenceChange`. That function is the read path
  for stored rows as well (`server/backend/database/change_info.go`), so a
  bound there would make a row stored before the bound unreadable.
- The JS SDK side: it matches the error code the same way the Go client does,
  but is a separate repository.
