# Remote redo of a restored key can delete it on a peer

**Created**: 2026-08-16

Found while building Task 7 (presence in the undo/redo history port,
`docs/design/undo-redo-go-port.md`) and confirmed in code review. Not fixed
here: it is a pre-existing defect in both `yorkie` and `yorkie-js-sdk`, and
per the port's own rule ("Port JS's known defects as-is") a one-sided fix in
Go alone would widen the gap the port exists to close. This document is the
filing so it can be picked up as its own task.

This document also records two smaller, related presence defects found in
the same investigation — see "Related: `Presence.Initialize` leaves
`clonePresences` stale" and "Related: Go's zero value vs. JS's dropped key
on undoing a newly introduced presence key" below. They are bundled here
rather than filed as separate pairs because all three surfaced from the same
Task 7 investigation, are all "record and defer" rather than active fixes,
and are individually too small to justify fragmenting the backlog.

## Problem: redo does not propagate correctly to peers (replica divergence)

**Severity: this is replica divergence, not a display bug.** A garbage
collection pass on a peer can delete a key that is still live on the
client that redid it, and the two replicas never reconverge on their own.

### Mechanism

1. A `Set` operation creates an element under a fresh `createdAt` ticket
   (call it `T1`) — e.g. `root.SetInteger("count", 1)`.
2. `Undo` replays the operation's reverse, a `Remove` targeting `T1`. On
   both the local replica and any peer that later applies the same
   `Remove` remotely, the element is tombstoned and registered for GC:
   `root.RegisterRemovedElementPair` populates `Root.gcElementPairMap[T1]`
   (`pkg/document/crdt/root.go:156-168`).
3. `Redo` replays `Remove.Execute`'s own reverse
   (`pkg/document/operations/remove.go:91-120`,
   `toReverseOperation`/`case *crdt.Object`) — a `Set` built from
   `NewSet(o.parentCreatedAt, key, copied, o.executedAt)`. `copied` is a
   deep copy of the *original* removed element, so it keeps the original
   `createdAt` (`T1`). This is the asymmetry the review flagged: `Add` and
   `ArraySet` reverses are re-ticketed with a fresh `createdAt` before
   redo replays them (`pkg/document/document.go:405-413`,
   `executeUndoRedo`'s `*operations.Add` / `*operations.ArraySet`
   branches), but a plain `Set`'s reverse is not — it is pushed back onto
   the context unchanged, still carrying `T1`.
4. `Set.Execute` (`pkg/document/operations/set.go:56-113`) applies the
   restore. `obj.SetWithExecutedAt(o.key, value, o.executedAt)` (line 95)
   makes the object's key resolve to the new, live element again. Then,
   **only when `source == OpSourceUndoRedo`** (lines 102-104):
   ```go
   if source == OpSourceUndoRedo && root.FindByCreatedAt(value.CreatedAt()) != nil {
       root.DeregisterElement(value)
   }
   root.RegisterElement(value)
   ```
   `DeregisterElement` (`root.go:122-154`) drops the stale entries for
   `T1` from both `Root.elementMap` and `Root.gcElementPairMap` before
   `RegisterElement` re-adds the live element under `T1`. On the client
   that performed the redo (`source == OpSourceUndoRedo`), this correctly
   clears the pending-GC entry for the identity being reused.
5. The redo `Change` is pushed to the server and pulled by a peer exactly
   as pushed — confirmed by direct inspection, the peer receives a
   `Change` with the operation intact. The peer applies it via
   `InternalDocument.ApplyChanges` (`pkg/document/internal_document.go:318`)
   with `source = OpSourceRemote`. The `source == OpSourceUndoRedo` guard
   at `set.go:102-104` does **not** fire, so `DeregisterElement` is never
   called on the peer. `Root.gcElementPairMap[T1]` on the peer still holds
   the *old*, tombstoned pair from step 2, even though the object's key
   now resolves to the live, restored element.
6. `Document.ApplyChangePack` runs GC automatically on every non-snapshot
   pull (`pkg/document/document.go`, the `if !d.options.DisableGC &&
   !hasSnapshot { d.GarbageCollect(...) }` step), so this is not a latent
   risk waiting for an explicit GC call — it fires on the very pull that
   receives the redo. `Root.GarbageCollect` (`root.go:184-211`) iterates
   `gcElementPairMap`, and for the stale `T1` entry calls
   `pair.parent.Purge(pair.elem)` followed by
   `r.deregisterElement(pair.elem)` — which deletes `elementMap[T1]`
   **by key**, regardless of which element (live or tombstoned) currently
   occupies that slot. The peer's live, just-restored key is deleted.
7. The two replicas now disagree permanently: the client that redid the
   change keeps the key; every peer that pulled the redo loses it on
   their next GC pass. Nothing in the protocol re-converges this — it is
   not a transient display bug.

This generalizes beyond `Object.Set`: any operation whose redo path
reuses an original `createdAt` instead of re-ticketing it is exposed to
the same asymmetry, since the `OpSourceUndoRedo`-gated deregister step
exists specifically to reconcile a *reused* identity, and remote
application always uses `OpSourceRemote`.

### Cross-SDK note

**JS has the identical gate.** `set_operation.ts:99-104` in
`yorkie-js-sdk` guards the same deregister call with `source ===
OpSource.UndoRedo` only — so this is not a Go-only bug, and Go's current
behavior is faithful parity with JS, not a regression. **A fix must land
in both `yorkie` and `yorkie-js-sdk`**, or one SDK's redo becomes silently
unsafe to sync to a peer running the other. Do not fix Go alone.

### Two fix directions (named in review, not evaluated further here)

1. **Make the deregister source-independent.** Drop the `source ==
   OpSourceUndoRedo` condition at `set.go:102-104` (and the JS
   equivalent) so any application — local undo/redo or remote — clears a
   stale entry for a reused `createdAt` before re-registering it. Needs
   checking whether this is safe for ordinary (non-undo/redo) `Set`
   operations that happen to collide with a tombstoned `createdAt` for
   unrelated reasons, and whether other operations (`Remove`, `Add`,
   `Move`) that touch `root.FindByCreatedAt`/`DeregisterElement` have the
   same gap.
2. **Re-ticket `Remove`'s reverse `Set` the way `Add`/`ArraySet` are.**
   In `executeUndoRedo` (`document.go:405-413` today), add a
   `*operations.Set` branch that issues a fresh ticket for the reverse
   before pushing it, mirroring the `Add`/`ArraySet` treatment, and call
   `History.ReconcileCreatedAt` for any other stacked operation that
   still points at the old identity. This avoids ever reusing a
   `createdAt` for a live element during redo, so `OpSourceRemote` never
   needs to know about the reuse at all. Needs the JS-side equivalent in
   `set_operation.ts`/`document.ts` (see the `TreeEdit` `reissueContentIDs`
   precedent, which already re-tickets on the `TreeEdit` redo path for a
   related reason).

Either direction needs the identical decision applied in
`yorkie-js-sdk`.

### Reproduction sketch

Two clients, plain `Object.Set`, no presence involved (confirms the bug
is not presence-specific — it was found while testing presence undo, but
reproduces without it):

```go
d1 := document.New(key)
c1.Attach(ctx, d1)
d2 := document.New(key)
c2.Attach(ctx, d2)

d1.Update(func(root *json.Object, p *presence.Presence) error {
    root.SetInteger("count", 1)
    return nil
})
c1.Sync(ctx); c2.Sync(ctx)
// d2.Marshal() == `{"count":1}` -- fine so far.

d1.Undo()
c1.Sync(ctx); c2.Sync(ctx)
// d2.Marshal() == `{}` -- undo's own Remove syncs correctly; this
// direction is not the bug.

d1.Redo()
// d1.Marshal() == `{"count":1}` locally, immediately, before any sync.
c1.Sync(ctx); c2.Sync(ctx)
// d2.Marshal() == `{}` -- the peer's key is gone, even though the pulled
// Change was confirmed (via ad hoc logging of InternalDocument.ApplyChanges)
// to carry the operation intact. d1 and d2 have now permanently diverged.
```

Extending this into a full regression test should additionally assert
`doc.GarbageLen()` / `Root.GCElementPairMap()` on both replicas after the
redo+sync to pin the stale-entry mechanism directly, not just the
end-state key loss.

## Tasks

- [ ] Reproduce with an explicit integration test in `yorkie` (both the
      end-state divergence and, ideally, the intermediate
      `gcElementPairMap` state) before attempting a fix
- [ ] Decide between the two fix directions above (or find a better one)
      for both SDKs — this needs its own design discussion, since
      direction 1 has an open safety question and direction 2 needs the
      equivalent JS change designed alongside it
- [ ] Check whether `Remove`'s `*crdt.Array` reverse (`Add`) and `Move`'s
      reverse have the same `OpSourceUndoRedo`-gated special-casing that
      would need the identical treatment (`remove.go`, `move.go`)
- [ ] Fix in `yorkie` and `yorkie-js-sdk` together; land both before
      either ships, or add a version gate
- [ ] Add the regression test from the reproduction sketch to both SDKs'
      undo/redo integration suites
- [ ] Re-tighten `test/integration/doc_presence_test.go`'s mixed
      op+presence redo assertion back to asserting on the peer (`d2`)
      instead of locally (`d1`) once fixed — see the comment referencing
      this document at that call site

## Related: `Presence.Initialize` leaves `clonePresences` stale

Also found while building Task 7, also pre-existing, also not fixed here.

`Presence.Initialize` (`pkg/document/presence/proxy.go`, used only via
`client.WithPresence` at attach) does `p.data = data` — a pointer
reassignment — instead of mutating the map already referenced by
`Document.clonePresences`, the way `Presence.Set` does (`data := p.data;
data.Set(key, value)`, mutating in place). A later `Update` call's `Set`
on an unrelated key then silently drops the attach-time key from both
`clonePresences` and, once that change executes,
`InternalDocument.presences`.

Reproduction:

```go
d1 := document.New(key)
c1.Attach(ctx, d1, client.WithPresence(presence.Data{"color": "red"}))
// d1.MyPresence() == {"color":"red"}

d1.Update(func(root *json.Object, p *presence.Presence) error {
    p.Set("other", "x")
    return nil
})
// d1.MyPresence() == {"other":"x"} -- "color" is gone.
```

This is the Go twin of a JS issue already tracked upstream: JS has a
`TODO(chacha912)` at `document.ts:2068-2069` reading "After resolving the
presence initialization issue, remove default presence.(#608)" — the
same root shape (an `{}`-seeded presence entry that a later real value
doesn't fully merge into) surfacing on the undo/redo context-construction
path. Fixing this in Go should be coordinated with resolving #608
upstream rather than treated as Go-only.

Worked around, not fixed: Task 7's integration tests seed presence
baselines through a plain `Update` + `Set` call instead of
`client.WithPresence`, so this defect is never exercised — see the
comment referencing this document in
`test/integration/doc_presence_test.go` at the test setup. This leaves
the combination `client.WithPresence` + `Set(..., WithHistory())` +
`Undo` entirely untested.

### Tasks

- [ ] Decide the fix shape: either make `Initialize` mutate `p.data` in
      place (matching `Set`'s pattern) or make `Document.Update` refresh
      `clonePresences` from the just-applied change's presence value
      rather than trusting the pre-call snapshot
- [ ] Coordinate with #608 in `yorkie-js-sdk` so the fix shape matches
      whatever resolves the upstream TODO
- [ ] Add `TestDocPresence`/`TestPresencelessDocument`-style coverage for
      `client.WithPresence` followed by a later `Update` that sets an
      unrelated key, asserting the original key survives
- [ ] Once fixed, restore the `client.WithPresence` + `Set(WithHistory)` +
      `Undo` combination Task 7 skipped, in
      `test/integration/doc_presence_test.go`

## Related: undoing a newly introduced presence key — Go keeps `""`, JS drops the key

Found while adding a test to pin this exact behavior (Task 7 follow-up,
Item 4). **Confirmed to differ between the two SDKs; deliberately left
unfixed** — see `pkg/document/history_test.go`,
"presence undo of a newly introduced key restores the zero value test",
which pins Go's current (divergent) behavior. Do not change Go's
behavior to "fix" this without also deciding what the wire format should
be, since this affects what gets sent to peers, not just local state.

`change.Context.ReversePresence()` builds the reverse from
`c.previousPresence[key]` for every key marked `WithHistory`
(`pkg/document/change/context.go`). When the key did not exist in
`previousPresence` — i.e., the presence-history entry is undoing a key
that was *introduced* by the tracked `Set`, not merely changed — Go's map
indexing yields the zero value, the empty string, so the key survives
with value `""` instead of being removed.

JS's equivalent, `getReversePresence()` (`context.ts:210-220`), assigns
`this.previousPresence[key]` too, which is `undefined` for a key that
didn't exist. That `undefined` reaches `Channel.set`'s
`deepcopy(this.presence)` call (`presence.ts:36-47`), and
`yorkie-js-sdk`'s `deepcopy` is `JSON.parse(JSON.stringify(object))`
(`util/object.ts:22-29`) — `JSON.stringify` omits object properties whose
value is `undefined`. So the key is dropped from the Put change's
`presence` payload entirely, and since a presence Put replaces the whole
map for the actor, the key is effectively *removed* on undo in JS.

Go's `presence.Data` (`map[string]string`) has no representation for "no
value" short of not being a key in the map at all, so matching JS exactly
would mean `ReversePresence` deciding, per key, whether to `Set` or
*omit* — a real behavior change, not something to do inside a "no
production code changes" bookkeeping pass. Filed here rather than fixed.

### Tasks

- [ ] Decide the target Go behavior: omit the key (matching JS's
      wire-level effect) vs. keep sending `""` (current). Confirm what
      `yorkie-js-sdk` actually sends on the wire for this case (a Put
      with the key absent) so the two SDKs choose the same behavior for
      parity, not just each independently "fixing" it
- [ ] If the target is "omit", design how `ReversePresence`/`SetOption`
      distinguish "no reverse value" from "reverse value is the empty
      string" in a `map[string]string`
- [ ] Update the pinning test in `pkg/document/history_test.go` once the
      target behavior is decided and implemented
