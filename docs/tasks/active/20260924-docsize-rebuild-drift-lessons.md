# docSize drifts from a rebuild in three places — lessons

**Created**: 2026-09-24

## The "one line" in item 3 was three

The issue proposed writing `""` into the tombstone `RHT.Remove` mints, and
called it one line. It is one line only if nothing downstream depends on the
tombstone being byte-identical to the value it replaced. Two things do:

- `Root.RegisterGCPair` subtracts `Child.DataSize()` from Live and adds the
  same number to GC. That is only a correct move-across because the tombstone
  currently charges exactly what the live node it replaced was charging. Shrink
  the tombstone and the value's bytes stay in Live forever — a permanent drift,
  strictly worse than the transient, self-healing GC divergence being fixed.
- `Root.collect` subtracts `Child.DataSize()` at purge, while the enclosing
  node's GC charge was taken when the node was removed and still included the
  then-live attribute. Shrink the tombstone and the same bytes strand in GC.

So `Remove` reports the dropped value's bytes and the two `RemoveStyle` callers
take them out of whichever ledger was holding them. The `nodeIsLive` flag those
callers already compute for `attrGCPair` is exactly the discriminator.

The general lesson: in this ledger a tombstone is not just a marker, it is the
receipt that lets a registration move a charge between two halves. Changing what
a tombstone weighs changes the arithmetic of every path that weighs it.

## Two tests were pinning the bug, not the fix

`TestBarrierRetentionIsBoundedByLagNotByArraySize` and
`TestBarrierCostsNothingOnConcurrentMoves` asserted that a fully collected
document costs exactly what it cost before the moves. That was true only because
the `movedAt` ticket was never charged. It is the right assertion for retention
and the wrong one for content: a moved element really does carry a ticket it did
not carry before, and a rebuild charges it.

Both now bound the residue at one ticket per moved element and additionally call
`assertRebuildsSame`, which is the invariant that cannot be satisfied by a
coincidence. A golden-number test that happens to encode a missing term reads as
a regression when the term is added; stating the invariant instead would have
made the fix land without touching them.

## A document-level test that looked like it covered the GC branch did not

The first attempt to cover "the attribute was stripped from a node that is
already a tombstone" built the obvious shape: two replicas, one deletes the
styled paragraph, the other strips the attribute, exchange. It passed — and it
still passed with the GC compensation deleted, which is how the gap showed.
Instrumenting the branch showed why: a `Tree`'s `traverseInPosRange` never
reaches a node the replica has already tombstoned, so only the replica that had
*not* deleted it ran the removal, and there the node was live.

`Text` is different — `findBetween` walks tombstones, and the comment on
`Text.RemoveStyle` says so. The branch is pinned there instead, at the CRDT
layer, with a range resolved before the delete so it still addresses the node
after it.

The lesson is procedural: a new test that passes is not evidence until it has
been run against the code without the fix. Both tests here were, and one of
them had to be rewritten as a result.

## Only half of item 2 is reachable from this repository

The issue offers two fixes for the server/SDK size disagreement and prefers the
SDK one. That one lives in `yorkie-js-sdk`. The Go half is implemented here and
the comments it reconciles are the SDK's, so the two repositories have to agree
on which half landed — noted in the PR rather than left to be discovered.

## Rounds

Self-review was not run: this was a one-shot autonomous run with no reviewer
subagent available. Verification is CI, `@claude review`, and a human.

## Panel round 1: the ledger a diff lands in is a second decision

Two of the three blocking findings were the same mistake in different places:
the change computed *how much* a write costs and then assumed *where* it goes.

`Move.Execute` charged the movedAt ticket to Live unconditionally, but a move
applied to an element another replica has already deleted is ordinary — RGA
stamps movedAt either way — and a rebuild charges a tombstone's whole DataSize
to GC. Routing it needs more than picking `AccGC`: `deregisterElement`
subtracts what `sizeInGC` recorded for that element, so a GC top-up that leaves
the record alone only moves the stray bytes from Live to GC. `Root.AccMovedElement`
raises the record with the charge; without it the test still failed, one ledger
over.

The decode boundary was the same shape read backwards. `Remove` minting a
valueless tombstone makes a tombstone's size a function of its key, but only
for tombstones this code minted; `SetInternal` restored whatever the wire
carried, so a pre-upgrade snapshot rebuilt a heavier tombstone than the replica
that performed the removal. Enforcing it in `SetInternal` covers both decoders
and `DeepCopy` at one choke point, which is the only place all three meet.

The reproduction rule from the round before held again: both new tests were run
against the code with the fix removed, and both failed with exactly the drift
the finding described (24 bytes stranded in Live after collection; a heavier GC
charge on the legacy payload).

## Reported, not fixed: the size limit has no server-side gate

The security lens is right that `MaxSizePerDocument` is enforced only in
`Document.Update` on the client, and that the server's push path never
re-checks it. It is also not something this change introduced or could close
here: `pushPack` stores changes without materializing a root, so a real gate
means building the document on the push path and deciding what a rejected push
does to a client that already applied it locally — a design question, not an
accounting one. Rebutted on scope with the note that the finding itself stands.

## Panel round 2: the same gap, re-raised — standstill, not a skip

Round 2 returned the identical finding. Nothing about it has changed and it is
still correct, so this round did not write code against it either. What the
second pass added was the cost of the only honest fix, which is what makes it a
design decision rather than a patch:

- The push path is deliberately document-free. `preparePack` returns before any
  document exists for push-only mode (`server/packs/pushpull.go:446`), and
  `pushPack` hands `pushables` straight to `CreateChangeInfos`
  (`server/packs/pushpull.go:316`) without a root.
- The only server-side way to obtain a root is `BuildInternalDocForServerSeq`
  (`server/packs/snapshot.go:67`): a snapshot load, a `FindChangesBetweenServerSeqs`
  range query, an `ApplyChangePack`, a `GarbageCollect`, and two whole-document
  `DeepCopy` calls. Today that runs only when a pull crosses the snapshot
  threshold. A gate in `pushPack` would run it on *every* push.
- Cheaper half-gates were considered and rejected as worse than nothing. Reading
  `DocSize()` off `be.Cache.Snapshot` is free but is never armed in the case that
  matters — `storeSnapshot` builds its own document and does not populate that
  cache, so a client that only pushes never warms it. Capping the bytes of a
  single pack bounds one push, not a quota reached over many.
- A lagging gate — have `storeSnapshot`, which already builds the document,
  persist the size on `DocInfo` so `pushPack` can reject cheaply — is the shape
  that would actually work. It needs a `DocInfo` field across both the Mongo and
  memory backends and an answer for the client that has already applied locally
  what the server now refuses. That is a design doc, not a review fix.

Recorded as a standstill rather than a silent skip: the finding is upheld, the
work is real, and it belongs to a human and a `docs/design/` entry.

## Panel round 3: the design entry the standstill was owed

Third pass, same finding, still correct, still not a patch. What was missing
from rounds 1 and 2 is that both parked the work in a task-lessons file, which
is where a decision goes to be forgotten. Round 3 wrote it down where the repo
keeps decisions instead: `docs/design/document-size-limit.md`, indexed from
`docs/design/README.md`, marked **proposal** so no reader mistakes it for
shipped behavior.

Writing it out surfaced one thing neither earlier round had stated, and it is
the reason a blanket gate cannot just be added:

- `DocSize.Total()` is `Live + GC` (`pkg/document/resource/resource.go:26`), and
  a deletion does not shrink `Total` — it moves bytes into `GC`, where they stay
  until every peer's version vector passes the tombstone. So a gate that refuses
  every push on an over-quota document deadlocks it: the push that would delete
  content is refused for the same reason as the push that added it. The gate has
  to refuse *growth*, define a recovery, or move the enforcement out of the push
  path entirely. That is three options with different SDK consequences, which is
  exactly what a design doc is for.

The rule this leaves: a standstill is only honestly recorded once it is recorded
somewhere a person who is not reading this PR would find it.

## Panel round 4: the size measurement moved off the hot path

Two findings came back. One was actionable and is fixed; the other is the same
standstill for the fourth time.

The actionable one: `logicalValue` ran `json.Unmarshal` on a client-supplied
attribute value *inside* `RHTNode.DataSize()`. That method is called per split,
style, register and collect, and over every node of a document on a rebuild, so
an O(1) size read had become an O(value) decode-and-allocate that a client
chooses the cost of. The leading-quote short-circuit did not help a value that
simply starts with a quote and never closes: it still paid a full scan and a
failed parse, every call.

The fix has three parts, and only the first is the one that matters:

- Measure once, at `newRHTNode`, and carry the result on the node (`valLen`).
  Nodes are immutable, so the measurement cannot go stale. `DataSize()` and
  `Remove`'s `ValueDropped` read the field.
- `RHT.DeepCopy` now copies nodes rather than replaying them through
  `SetInternal`, so a clone carries the measurement across instead of
  re-measuring every attribute. A clone is taken on every local update, so
  rebuilding there would have reintroduced the same cost one level up.
- `logicalSize` decodes only when it must: an unquoted value costs two byte
  compares, and a quoted body with no escape, bare quote or control character
  decodes to itself and is measured by subtraction.

The rule: a `DataSize()` that is cheap to call is part of its contract, not an
implementation detail. Anything derived from untrusted bytes belongs at the
construction boundary, where it is paid for once against a string the parser
already allocated.

The equivalence is pinned by `rht_logical_size_test.go`, which asserts
`logicalSize` against a spelled-out decode-always reference over the hostile
inputs as well as the ordinary ones — including the trailing-whitespace case,
where JSON permits what the fast path must not silently charge differently.

### The size-limit gap, round 4 (superseded by round 5)

Still `--skipped`, still recorded as a standstill and not a disagreement: the
refusal semantics of a server-side gate is a wire-protocol decision about what
a client does with a push the server refuses after the client already applied
it, and that is not a reviewer's call to make inside a fix pass.

What this round added to `docs/design/document-size-limit.md` is one fact that
makes option 2 cheaper than rounds 2 and 3 assumed: the deadlock those rounds
treated as disqualifying already exists client-side. `Document.Update` compares
the post-update `Total()` (`pkg/document/document.go:257-258`), so a stock SDK
already refuses a *deletion* on an over-quota document. A blanket server
refusal would not be inventing a new failure mode, only requiring a way to
express the existing one over the wire.

## Panel round 5: the standstill was cheaper than four rounds of arguing it

Rounds 1–4 skipped the server-side size gate on the grounds that its refusal
semantics were a wire-protocol decision. Round 5 wrote the gate. The thing that
unblocked it was not new information; it was reading the option the earlier
rounds had already written down and noticing it needs no wire change at all.

Option 1 from `docs/design/document-size-limit.md` — refuse *growth*, admit
deletions — sidesteps the deadlock that made rounds 2 and 3 call a gate
undeliverable, and sidesteps the "no rejection message exists" problem that
round 4 called terminal:

- The deadlock argument was about `DocSize.Total()` being `Live + GC`, so a
  deletion does not shrink it. True, and exactly why the gate must not refuse
  deletions. It does not: `mayGrowDocument` admits `Remove`, content-free
  `Edit`/`TreeEdit`, and presence-only changes.
- The "client already applied it locally" argument assumed the refused client
  is an honest one. It cannot be. The server's number is the one `storeSnapshot`
  last measured, so it lags; the client's is exact and compares against the same
  limit, so an honest client's own check always trips first. The only client
  this gate ever refuses is one that skipped the local check — which is the
  threat model, and which is owed nothing better than a terminal error.

Three implementation facts that were not obvious from the design sketch:

- The classification has to run on `reqPack.Changes`, not on the `ChangeInfo`s
  that `pushPack` builds: a `ChangeInfo` carries its operations already encoded,
  so classifying from one would mean decoding on the hot path. `pushPack` now
  carries both slices and keeps them in step, including where an epoch mismatch
  clears `pushables`.
- The size write must not join the push path's compare-and-set. `UpdateDocInfoSize`
  is a bare `$set` on `doc_size` and *removes* the `docCache` entry rather than
  re-adding a `DocInfo` it read outside `DocPushKey` — re-adding is what the
  NOTE at the top of `pushPack`'s locked block warns produces
  `ErrConflictOnUpdate`.
- Returning `connect.NewError(...)` would have silently dropped the error code.
  `connecthelper.ToStatusError` returns an already-`connect.Error` untouched, so
  the `ErrorInfo` metadata carrying `ErrDocumentSizeExceedsLimit` is only
  attached if the handler returns the bare `StatusError`.

The rule this leaves: a standstill recorded four times is a decision not being
made, not a decision being deferred. When the design doc a previous round wrote
already contains a viable option, the next round's job is to build it, not to
re-describe why building it is hard.
