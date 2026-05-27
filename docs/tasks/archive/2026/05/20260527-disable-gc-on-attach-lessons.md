**Created**: 2026-05-27

# Disable GC on Attach — Lessons

## Question persistence before reaching for it

The first cut persisted `disable_gc` on `ClientDocInfo`. The reasoning at the
time was that four RPCs reach `packs.PushPull` (`AttachDocument`,
`PushPullChanges`, `DetachDocument`, `RemoveDocument`, plus the cluster
server) and centralising the flag would avoid fan-out. That reasoning was
sloppy in two ways:

- `DetachDocument` and `RemoveDocument` are terminal: one extra `minVV`
  write per session is not worth carrying state for.
- Only `AttachDocument` and `PushPullChanges` are hot paths, and adding
  the flag to two proto messages is a much smaller surface than the
  persistence path (new BSON field, signature change on `AttachDocument`,
  ~30 test call-site updates, `DeepCopy` update, memory backend write
  preservation, mongo `$set`, mongo cache short-circuit equality).

**Rule**: when choosing between request-time and persisted state for a
boolean, count the actual touch points on both sides. "Centralised" is
not the same as "smaller." Persisted schema fields are forever; per-request
state isn't.

The user prompted the rework with one short question. That cost zero
review cycles compared to landing the persisted version. Cheap questions
beat expensive cleanup.

## Don't reset `DisableGC` on Detach when re-attach already overwrites it

(Captured against the persisted version of the design and superseded by
the per-request approach; kept here because the principle is general.)

The first persisted cut explicitly cleared `DisableGC` in
`DetachDocument` and `RemoveDocument` as defense-in-depth. The clear was
redundant: `AttachDocument` replaces the entire `ClientDocInfo` struct,
and `$set` on mongo overwrites the persisted field on the next PushPull.

**Rule**: when a struct field is read only between two state
transitions that both fully rewrite the struct, an explicit clear in
between is dead code. Lean on the rewrite invariant; comment the
reasoning instead of duplicating the clear.

## `GetMinVersionVector` with no stored rows returns the caller's VV unchanged

`MinVersionVector` ignores missing keys by yielding 0, but when only
one vector is supplied (the caller's `doc.VersionVector()`), every key
present in it survives the min computation. The original design note
that all-opt-out documents would GC "conservatively (collects nothing)"
was wrong: with no stored rows the server-side snapshot GC actually
runs against the document's max VV, which is the aggressive behavior
we wanted. Useful to know if anyone later tries to design an explicit
aggressive path — it already exists for free.

## `make proto` regenerates files my proto change did not touch

Running `buf generate` regenerated `yorkie.connect.go` with a
different `connect.IsAtLeastVersion` marker and added `PeekChannel`
entries to `yorkie.openapi.yaml` that did not exist in the committed
file. The local toolchain version differs from whatever produced the
committed artifacts. Restored those files to `HEAD` and committed only
the `.proto` and `.pb.go` that the new field genuinely required.

## Open follow-ups

- Wire-compat behavior (old/new client × old/new server) not yet
  exercised against a real release pair. Worth a smoke test before the
  JS SDK PR.
- `vv-cleanup` interaction (detached-actor signaling) was not tested
  in the presence of opt-out attachments. Likely safe because
  `detached_actors` signaling is independent of stored VV rows, but
  worth confirming.
- Go SDK option lives next to existing `document.WithDisableGC`. The
  package qualifier disambiguates, but the two flags do different
  things and a future reader could confuse them. Mention this in the
  JS SDK PR so the JS naming explicitly distinguishes the wire flag
  from any local-GC option.
- Deferred client-side optimizations: skip serializing `reqPack.VersionVector`
  when `disableGC=true`, and skip the local `GarbageCollect` traversal
  when the response VV is empty. Both are correctness-preserving with
  the current code but worth picking up if profiling shows the cost.
