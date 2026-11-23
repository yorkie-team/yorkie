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

Bound a presence `PUT` the client pushes, measured in the same `presenceSize`
the other two bounds use, inside the walk `foldPresencePatches` already makes
over the changes `pushPack` will store. Only those changes — the ones above
the client's checkpoint — are measured, so a resend that repeats an
already-acked change is not touched.

Per put: `maxPresencePutSize`, 64 KiB, the same 16 x `maxPresenceBaseSize` the
pack fold budget uses. It cannot be `maxPresenceBaseSize` itself: a presence
over 4 KiB is a supported, if degraded, case today — such a client is never
folded into nor cached and pushes full puts instead (the motivating app of
#2154 held about 7.4 KB) — so bounding at 4 KiB would break clients that work
now. 64 KiB is well above any presence a cursor or a selection needs and
bounds the per-peer fan-out of one push to a constant.

Per pack: `maxPresencePutPerPack`, 256 KiB, 4 x the fold budget. The per-put
bound alone leaves the same amplification available to a pack of a few
hundred puts at the bound, since only clientSeq continuity limits how many
changes a pack carries.

Neither bound refuses the push. A client keeps a local change until the
server acknowledges it, so a refused pack is pushed again unchanged: the
refusal would be permanent rather than something a smaller presence clears,
and the document — operations included — would stop syncing for good, taking
the detach and remove packs with it. An oversized put is stored without its
presence, and the cached base goes with it so the client's next patch is not
folded onto a presence the sender no longer holds; puts past the pack budget
share the put of their run's last change, as the fold's own budget does.

## Plan

- [x] `maxPresencePutSize` and `maxPresencePutPerPack` in
      `presence_patch.go`, enforced in the `presence.Put` arm of
      `foldPresencePatches`.
- [x] Internal tests: a put at the bound is stored as sent; one over it loses
      its presence, drops the base and refuses the patch that follows it; a
      put of many tiny keys is dropped; puts past the pack budget share the
      put of their last change; a put at or below the checkpoint is left
      alone; a detach pack carrying an oversized put is not refused.
- [x] An integration test driving an oversized put through `AttachDocument`,
      so the push is checked against the real RPC surface and a peer's pull
      shows the presence was not stored.
- [x] `docs/design/presence-patch.md`: a "Put size" section, the risk rows
      and the decision row.

## Out of scope

- A configurable per-project presence quota. The bound is a constant like the
  other two; making presence a project-level quota is a separate decision
  about what `MaxSizePerDocument` measures.
- Enforcing in `converter.FromPresenceChange`. That function is the read path
  for stored rows as well (`server/backend/database/change_info.go`), so a
  bound there would make a row stored before the bound unreadable.
- Any SDK change. Neither bound returns an error, so no client has to learn
  a new code; a client that wants to know its presence was too large to fan
  out would need a reply field, which is a separate decision.
