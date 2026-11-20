# The presence fold's size bound is per put, not per pack

**Created**: 2026-11-18

Follow-up to the review of #2227, filed as #2229. The code is outside that
PR's diff and the finding is independent of it.

## Problem

`maxPresenceBaseSize` (4 KiB) bounds *one* folded put. A pack carries any
number of changes, and `foldPresencePatches`
(`server/packs/presence_patch.go`) rewrites every presence `PATCH` in it into
a full `PUT` of the sender's presence. Nothing bounds the pack.

So a client can park a presence just under 4 KiB and then push a single pack
of N minimal patches — each ~50-80 bytes on the wire, needing only clientSeq
continuity (`validateClientSeqContinuity`, `pushpull.go`). Each one is
rewritten into a 4 KiB put, stored through `CreateChangeInfos` and pulled by
every other client of the document. Presence is excluded from every document
size gate (`checkDocumentSize` measures operations), so the only ceiling left
is the 16 MiB `WithReadMaxBytes` on the RPC server: on the order of 10^5
folded puts × 4 KiB written and fanned out from one request — roughly two
orders of magnitude of write and fan-out amplification.

The design doc states the mitigation as "a folded put costs at most that",
which is true per put and not per pack, so the hazard is understated exactly
where an operator would look for it.

## Fix

Give the fold a per-pack byte budget, `maxPresenceFoldPerPack`, measured in
the same `presenceSize` the per-put bound uses, and charge every folded put
against it. Past the budget the walk keeps folding but stops writing a put
per patch: the patches of the run lose their presence and the last of them
carries one `PUT` of what the run produced.

The push still lands. Refusing it with `ErrPresenceBaseUnavailable`, the way
a missing base does, would not converge: that error names no patch, and
passing the budget is a property of the pack's shape rather than of server
state, so the resend is over the budget too and the whole push — operations
included — is refused again. Coalescing removes the amplification outright:
a pack's folded output is the budget plus one put however many patches it
carries.

Budget: 64 KiB (16 × `maxPresenceBaseSize`). It bounds a pack's folded output
to a small constant regardless of how many patches it carries, while leaving
room for a batched pack of a few hundred ordinary-sized presences (the ~250 B
cursor presence of the motivating issue folds ~260 times within it).

## Plan

- [x] `maxPresenceFoldPerPack` in `presence_patch.go`, charged in the
      `presence.Patch` arm of `foldPresencePatches`.
- [x] Internal tests: patches over the budget share one put and keep the
      push; a put past the budget starts a new run; a pack under the budget
      folds every patch; the budget counts folded puts only.
- [x] `docs/design/presence-patch.md`: the fold table row, a "Fold budget"
      section and the risk row.

## Out of scope

- A cap on the number of changes in a pack. The amplification is what this
  fixes; a pack of N full puts is self-paying (the client sends the bytes it
  makes the server store) and bounding it is a separate, configuration-shaped
  decision about `MaxSizePerDocument`-style quotas for presence.
- Putting presence into a document size gate. That changes what a gate
  measures for every document.
