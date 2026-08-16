# Remote redo of a restored key can delete it on a peer

**Created**: 2026-08-16

Found while building Task 7 (presence in the undo/redo history port,
`docs/design/undo-redo-go-port.md`) and confirmed in code review. Not fixed
here: it is a pre-existing defect in both `yorkie` and `yorkie-js-sdk`, and
per the port's own rule ("Port JS's known defects as-is") a one-sided fix in
Go alone would widen the gap the port exists to close. This document is the
filing so it can be picked up as its own task.

This document has since become the collecting point for every smaller
"record and defer" finding the undo/redo port turns up — see the "Related:"
sections below, contributed by Tasks 7, 9 and 10. They are bundled here
rather than filed as separate pairs because they are all deferrals rather
than active fixes, and are individually too small to justify fragmenting
the backlog. Each carries its own reachability analysis and task list.

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

## Related: `Text.Style`/`RemoveStyle` do not skip tombstoned nodes, unlike JS

Found while building Task 9 (widening `Text.Edit`/`Text.Style`/
`RemoveStyle`'s CRDT-layer return values,
`docs/design/undo-redo-go-port.md`), confirmed pre-existing at base
`9395a96a` (i.e. present before Task 9 touched this code — Task 9 only
added new return values, it did not add or remove this filter). Not
fixed here, for the same reason as the rest of this document: per the
port's "port JS's known defects as-is" rule, a one-sided fix in Go alone
would widen the gap the port exists to close, and a cross-SDK behavior
change needs its own design decision, not a silent fix inside an
unrelated bookkeeping task.

JS's `setStyle` and `removeStyle` both skip a node entirely — neither
applying the attribute change nor considering it for the "previous
attribute" capture — when the node is already tombstoned:

```ts
// packages/sdk/src/document/crdt/text.ts:409 (setStyle)
for (const node of toBeStyleds) {
  if (node.isRemoved()) {
    continue;
  }
  ...
}
// packages/sdk/src/document/crdt/text.ts:504 (removeStyle)
for (const node of toBeStyleds) {
  if (node.isRemoved()) {
    continue;
  }
  ...
}
```

Go's `Text.Style` and `Text.RemoveStyle` (`pkg/document/crdt/text.go:508`
and `:604`) have never had this filter. Both loop over every node
`canStyle` accepted — which, per `canStyle`'s own logic
(`pkg/document/crdt/rga_tree_split.go`, `canStyle`), can include a node
tombstoned *before* `editedAt` — and apply the attribute change (or
attribute removal) to it regardless of tombstone state. So Go's `Style`/
`RemoveStyle` can mutate a removed node's attributes where JS's would
not.

### Why Task 9 didn't adopt JS's filter for the new `PrevAttr` capture

Task 9 added a `PrevAttr` capture (the value a style key held immediately
before the call, for reverse-operation construction) to both methods. The
capture is deliberately **not** filtered by `node.removedAt`, matching
Go's existing (unfiltered) attribute-application loop rather than
introducing a new, JS-only removed-node check that exists nowhere else in
these functions. The reasoning: the capture should reflect the node the
function's own attribute-set/remove logic actually touches first, so the
"previous value" it reports is self-consistent with what the forward
operation just changed on this replica — introducing a filter only for
the capture, while leaving the surrounding attribute-application loop
unfiltered, would make the two inconsistent with each other in a new way,
on top of the pre-existing Go/JS inconsistency. This was a "don't make it
worse while touching adjacent code" call, not an attempt to resolve the
underlying divergence.

### Tasks

- [ ] Decide whether Go should adopt JS's tombstoned-node skip in
      `Text.Style`/`Text.RemoveStyle`, or whether JS should drop it (JS
      is the older behavior here, so the default assumption is Go should
      match JS, but confirm there isn't a reason JS's skip exists that
      would make it the one to remove)
- [ ] Check whether `Tree.Style`/`Tree.RemoveStyle`
      (`pkg/document/crdt/tree.go`) have the same asymmetry, since they
      share the `canStyle`-based selection pattern
- [ ] If Go adopts the filter, re-verify `Text.Style`/`RemoveStyle`'s
      `PrevAttr` capture (`pkg/document/crdt/text.go:508`, `:604`) still
      captures from the correct (now filtered) first node
- [ ] Fix in `yorkie` and `yorkie-js-sdk` together, or add a version gate,
      per this document's usual rule for cross-SDK behavior changes

## Related: `validateRestoreIdentities` can reject a client's own undo

Found while building Task 10 (the Text `Edit` reverse operation,
`docs/design/undo-redo-go-port.md`) and sharpened in code review. Not
fixed there: loosening a security control is not a decision to make
inside a feature task.

`operations.Edit.Execute` validates every restore/retombstone span's
`createdAt` against the acting change's version vector
(`pkg/document/operations/edit.go`, `validateRestoreIdentities` /
`validateRestoreTickets`): the span's actor must be present in the vector
and its lamport must not exceed the actor's known clock, or the operation
fails with `ErrUnknownRestoreIdentity`. The guard skips only when the
version vector is empty, which its comment describes as "the trusted local
path (json package application)".

This is **Go-only hardening**. A grep of `yorkie-js-sdk/packages` for
`validateRestore` and `UnknownRestoreIdentity` returns zero hits, so JS
clients never perform this check on themselves.

Task 10 is what makes it live on the local undo path. Before it, Go could
only ever *execute* restore operations that arrived from a JS client (an
untrusted, remote input, which is what the guard was written for). Now Go
produces them locally, and `Change.Execute` passes `c.ID().versionVector`
— which for a local change is **not** empty (verified: a plain
`document.New` plus two `Update`s yields `{000...000:1}` then
`{000...000:2}`), so the guard runs against the client's own undo.

### Reachability (narrower than "any pruned actor")

The failure needs a version vector that is missing an actor whose content
the client is trying to restore. Most ways a VV changes cannot produce
that:

- `VersionVector.Unset` (`pkg/document/time/version_vector.go:115`) and
  `Filter` (`:266`) would drop entries, but neither has a non-test caller.
- `ID.SyncClocks` (`pkg/document/change/id.go:109`) and `ID.SetClocks`
  (`:145`) only `Max`-merge, so they never remove an actor.

The reachable case is **`ID.SyncLamport`** (`pkg/document/change/id.go:131-141`),
the GC-disabled attach mode (`docs/design/disable-gc-on-attach.md`). It
deliberately advances the lamport *without* merging the other side's
version vector, so such a client's VV stays at a single entry — its own
actor. A GC-disabled client that deletes text another actor inserted and
then calls `Undo` hits `versionVector.Get(otherActor)` returning `!ok`,
and gets `ErrUnknownRestoreIdentity` instead of a restore.

### Update: this now covers Tree undo as well, and is a pre-merge check

Added while building the Tree `Edit` reverse operation. `Tree.Edit` now
produces identity spans and `operations.TreeEdit.toReverseOperation`
short-circuits to a restore-mode reverse whenever they are complete, so
**the identity-preserving path is what an ordinary local Tree undo takes**,
not a corner reached only by operations arriving from a JS client. Every
such undo runs `validateTreeRestoreIdentities`
(`pkg/document/operations/tree_edit.go`), which shares
`validateRestoreTickets` with the Text path described above — so the same
`ErrUnknownRestoreIdentity` failure now applies to `Tree` undo under the
same conditions.

This raises the severity from "note" to a check to run before this port
merges: the exposure moved from a rarely-taken branch to the default
behavior of Tree undo.

Mitigating evidence, so the scope is not overstated: ordinary cross-actor
identity restore is covered and passes.
`test/integration/history_tree_test.go`'s
`TestHistoryTreeConcurrentUndo` has client `d2` undo a deletion of tree
content created by `d1`'s actor, and the restore succeeds — a normally
attached client's version vector knows the other actor, so the guard does
not fire. The residual exposure is exactly the VV-pruning case already
described above (`ID.SyncLamport`, the GC-disabled attach mode), now
reachable through Tree as well as Text.

### Tasks

- [ ] Reproduce with a GC-disabled attachment: two clients, client B
      opts out of GC, B deletes text A inserted, B undoes — assert the
      error today, then the restore once fixed
- [ ] Repeat the same reproduction for `Tree` (B deletes tree content A
      inserted, B undoes), since Tree undo now takes this path by default
- [ ] Decide the fix shape. The guard exists to stop a client forging a
      node under another actor's clock, so simply dropping it is not an
      option. Candidates: gate it on `OpSource` (remote input only,
      leaving locally produced undo alone), or make the GC-disabled path
      carry enough of a version vector to validate against
- [ ] Decide whether JS should gain the equivalent check rather than Go
      losing it — the asymmetry means a JS client can send the server a
      restore its own SDK never examined
- [ ] Whatever is chosen, keep `test/integration` coverage for a forged
      identity being rejected on the remote path, for both Text and Tree

## Related: the no-op fallback `Edit` reverse breaks on a remote replica

Also found while building Task 10, in code review. **Present identically
in JS, so not fixed** — per this document's usual rule.

`Edit.toReverseOperation` (`pkg/document/operations/edit.go`) has a
fallback for an edit that neither removed nor inserted anything — e.g.
`text.Edit(2, 2, "")`. Its reverse is an ordinary (non-restore) `Edit`
anchored at the **normalized** from position: the head node's id plus an
absolute offset from the head. The port of `edit_operation.ts:300-323`.

An `Edit` carrying normalized positions is only executable after
`RefinePos` remaps them onto the current chain, and that step is gated on
`isUndoOp` — which is **local-only state, never serialized** (Go has no
wire field for it; JS's `converter.ts` has zero `isUndoOp` references
either). So:

1. The undoing client executes it fine — `isUndoOp` is set in memory, the
   positions get refined, the no-op applies.
2. The change is appended to `localChanges` and pushed (the operation did
   execute, so `executeUndoRedo`'s "nothing executed" early return does
   not fire).
3. The server and every peer decode it with `isUndoOp` absent, take no
   refine step, and feed `(initialHead, N)` straight into
   `findNodeWithSplit`. `getAbsoluteID()` yields `(InitialTicket, N)`,
   whose floor is the head node, giving `relativeOffset = N` against a
   node of length 0 — so `splitNode` returns "offset should be less than
   or equal to length" (`pkg/document/crdt/rga_tree_split.go`) and the
   whole change fails to apply.

For any `N > 0` — i.e. any no-op edit that is not at the very start of
the text — this is not a cosmetic problem: it fails the peer's
`ApplyChanges`, not just the one operation.

### Tasks

- [ ] Confirm the peer-side failure end to end with an integration test
      (client A does `Edit(2, 2, "")` then `Undo`, client B syncs), in
      both SDKs
- [ ] Decide the fix shape, then apply it to both SDKs together. Options:
      put the fallback reverse's positions in un-normalized form; add
      `isUndoOp` to the wire format; or drop the fallback reverse
      entirely, since an edit that changed nothing arguably has nothing
      to undo — note this last one changes undo-stack depth, which is
      observable
- [ ] Whichever is chosen, check the same question for the restore-mode
      reverses, which also carry normalized positions but never resolve
      them positionally (identity addressing) except in
      `findRestoreAnchor`'s fallback rung, which does refine first

## Related: a text node's tombstoned attribute is resurrected by a snapshot round trip

Found while building the Text `Style` reverse operation (the reverse of a
`Style` call that added a key which did not exist before removes that key
via `RHT.Remove`, tombstoning it), while adding the round-trip coverage
this document's usual pattern calls for. **Present identically in JS, so
not fixed** — per this document's usual rule.

A text node's per-character style attributes are an `RHT` (Go:
`pkg/document/crdt/text.go`'s `TextValue.attrs`; JS:
`CRDTTextValue`'s `attrs`), the same structure used for object keys and
tree node attributes. Snapshot encoding writes every attribute node
regardless of tombstone state (Go: `toTextNodes`,
`api/converter/to_bytes.go:246-259`, iterating
`value.Attrs().Nodes()` unfiltered; JS: `toTextNodes`,
`packages/sdk/src/api/converter.ts:756-780`, iterating the RHT's
unfiltered `Symbol.iterator`), but for text nodes specifically — unlike
the sibling `toRHT`/object-and-tree path (Go has no equivalent generic
helper here; JS's `toRHT`, `converter.ts:803-816`) — the encoder never
sets the wire's `is_removed` field on `NodeAttr` (Go:
`api.NodeAttr.IsRemoved`, `api/yorkie/v1/resources.pb.go:1232`; JS:
`PbNodeAttr.isRemoved`). The decoder matches this gap symmetrically: Go's
`fromTextNode` (`api/converter/from_bytes.go:382-415`) calls
`attrs.Set(key, pbAttr.Value, updatedAt)` unconditionally, and JS's
`fromTextNode` (`converter.ts:1278-1290`) calls
`textValue.setAttr(key, value.value, ...)` unconditionally — both always
construct a live node, regardless of what the tombstone state was before
encoding.

So: style an attribute, remove it (directly, or as this port's `Style`
undo removing a newly added key), snapshot-encode, decode. The decoded
replica has the attribute back, live, at whatever value it held at the
moment of removal — even though the pre-snapshot replica correctly showed
it absent. This is not observable through the ordinary wire protocol
(`toRHT`/`fromRHT`, used for individual `Style`/`RemoveStyle` operations
and for object/tree attributes, carries `is_removed` correctly) — only
the snapshot path for `Text` specifically is affected, since that is the
one place a text node's full current attribute state, tombstones
included, has to survive being written out and read back rather than
being replayed as a sequence of operations.

Confirmed pre-existing and unrelated to this port: `toTextNodes` and
`fromTextNode` predate this branch by years on both sides (Go:
`git blame` on `to_bytes.go`'s `toTextNodes` shows the attribute-copying
loop from 2020, untouched by this port's commits; the `attrs :=
make(map[string]*api.NodeAttr)` line is from 2023, also unrelated). The
port's own `Style`/`RemoveStyle` `PrevAttr` capture (introduced widening
the CRDT-layer return values) does not touch encoding at all, so this bug
was reachable before this port started — the round-trip test just needed
a removed-attribute case to surface it, which nothing before did.

### Tasks

- [ ] Confirm the same gap for `RGATreeSplitNode`-adjacent tombstoned
      attributes doesn't already have separate handling elsewhere (e.g.
      GC purge of the RHT node itself, which removes it from the map
      entirely and would sidestep this — check whether that GC pass runs
      before every snapshot or is best-effort)
- [ ] Decide the fix: add `is_removed` to the text-node attribute
      encode/decode path on both sides, mirroring `toRHT`/`fromRHT`
- [ ] Fix in `yorkie` and `yorkie-js-sdk` together, or add a version gate,
      per this document's usual rule for cross-SDK behavior changes
- [ ] Add regression coverage: style an attribute, remove it, snapshot
      round trip, assert the attribute stays absent -- in both SDKs

## Related: a Tree reverse can delete live neighbours when its content was born tombstoned

Found while building the Tree `Edit` reverse operation. **Present in JS
too for the range width; Go additionally differs in where the range
starts.** Not fixed, per this document's usual rule.

`operations.TreeEdit.toReverseOperation`'s copy-reinsert fallback
(`pkg/document/operations/tree_edit.go`) builds the reverse of an edit as
"delete the range this edit inserted, re-insert what it removed". The
range's width comes from `TreeEditReverseInfo.InsertedContentSize`, which
`crdt.Tree.Edit` measures right after `dropDuplicateContents` and
**before** the insert loop runs — mirroring `crdt/tree.ts:2194-2196`, which
does the same and says so (`tree.ts:2188-2190`).

Measuring there is deliberate and correct: `TreeNode.remove` decrements
its *ancestors'* visible length, so a subtree inserted under an
already-removed parent shrinks its own root to zero as its children are
tombstoned. Reading the size back off the tree afterwards would report 0
for content that really was inserted.

The consequence is that when the insert position resolves inside a parent
a concurrent edit removed, every inserted node is tombstoned on the way in
(`fromParent.IsRemoved()` in `Tree.Edit`'s Phase 8), yet
`InsertedContentSize` still counts it. The reverse's range is therefore
`[idx, idx + size)` over content that occupies **no** visible index. JS's
only protection is the size guard at `tree_edit_operation.ts:610-616`
(ported as `if idx+insertedSize > tree.Root().Len() { return nil, nil }`),
which catches this **only when the range happens to run past the end of
the visible tree**. When there is enough live content to the right of the
anchor, the range instead covers live neighbours, and undoing the edit
deletes them.

### Why this is only partial JS parity

The *width* is JS's, verified. The *start* is not:

- JS anchors at `preEditFromIdx`, the from-index `CRDTTree.edit` captured
  before the deletion (`crdt/tree.ts:1872`).
- Go's `crdt.Tree.Edit` does not report `preEditFromIdx`, so
  `toReverseOperation` derives the anchor from the nodes the edit touched.
  On this path `lastLive == nil` (all content is tombstoned), so the anchor
  falls through to `info.Removed[0]`, or — when the edit removed nothing
  either — to the zero-width no-op reverse at `e.from`.

That second fallback is the separately-filed no-op-anchor divergence
(see "the no-op fallback `Edit` reverse breaks on a remote replica" for
the Text twin of the same shape). **The two defects compound here**: the
range this path produces can be wrong in width *and* in start, and the
size guard only reasons about the width. Any fix has to settle the anchor
question first, or the guard will keep being evaluated against an index
JS never intended.

### Reachability

Concurrency only. It needs `fromParent.IsRemoved()` at insert time — an
insert whose declared parent another client removed concurrently — which
also makes `SpansComplete` false (the born-removed nodes register GC pairs
past the delete-loop snapshot), so the edit takes the copy-reinsert
fallback rather than the identity-preserving reverse. Single-client
editing never reaches it.

### Tasks

- [ ] Reproduce with two clients: A removes an element, B concurrently
      inserts into it, B undoes — assert the live neighbour survives.
      Do it in both SDKs, since JS shares the width defect
- [ ] Decide the anchor question first (`preEditFromIdx` vs. the
      node-derived anchor); it gates whether the size guard is even
      checking the right thing. Reporting `preEditFromIdx` from
      `crdt.Tree.Edit` is the obvious option, and would also close the
      no-op-fallback anchor divergence filed above
- [ ] Then decide the width fix: skip the reverse whenever the tree
      accepted content but none of it is visible (`lastLive == nil &&
      InsertedContentSize > 0`) is the narrow option; widening JS's guard
      to the same condition is the cross-SDK one
- [ ] Fix in `yorkie` and `yorkie-js-sdk` together, or add a version gate,
      per this document's usual rule for cross-SDK behavior changes

## Related: a combined Tree `Style` reverse only restores, never removes, on execute

Refiled to its own task, since it is convergent (every replica drops the
removal identically) rather than divergent like this document's other
entries: see
`docs/tasks/active/20260816-tree-style-combined-reverse-dropped-todo.md`.
