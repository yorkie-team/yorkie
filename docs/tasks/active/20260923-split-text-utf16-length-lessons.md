# Lessons: SplitText records the left piece's length in runes

- **One unit everywhere.** The tree's lengths and offsets are UTF-16 code
  units because they have to agree with the JS SDK's string indices. A single
  site computing in runes is invisible for BMP text, which is most test data,
  and fatal for the first emoji or flag.
- **Test text outside the BMP.** A string with a surrogate pair in the middle
  exercises every place where runes and code units could be confused; plain
  ASCII and Hangul do not.

## Review round 1 (panel)

- **Correctness (major), accepted.** Splitting at an offset inside a surrogate
  pair let `utf16.Decode` rewrite the character as U+FFFD in both halves, so the
  replica kept text no other replica has. A test that skips those offsets pins
  nothing. Restoring the JS SDK's lone-surrogate halves is not possible in a Go
  string, so the first attempt reported `ErrSplitInSurrogatePair`.

## Review round 2 (panel)

- **Failing is not an answer for a position already in history.** An offset
  inside a surrogate pair is producible by a JS replica, so it can already sit
  in a stored change. `Change.Execute` aborts the whole change on any error but
  `ErrOperationSkipped`, and every server replay path propagates it, so the
  guard from round 1 turned "two replacement characters" into "this document
  can never be loaded again" — the outcome `docs/design/tree.md` names as the
  one to avoid. The same reasoning that justified `ErrSplitOutOfRange` does not
  carry over: that guard replaced a slice-out-of-range panic, this one replaced
  a split that worked.
- **Move the position instead of refusing it.** `SplitText` now moves a
  mid-pair offset forward to the end of the pair. Nothing fails, no character
  is corrupted, both pieces keep their lengths in UTF-16 code units, and every
  Go replica moves the same offset to the same boundary, so segmentation still
  converges. Reaching the node's end that way is the existing "nothing to split
  off" no-op.
- **The direction is load-bearing, and only the test at document level caught
  it.** Moving *back* looked equally valid and passed the `SplitText` unit
  test, but an edit resolves the same anchor twice: `from` split the node at
  the earlier boundary, then `to` re-resolved, found the fresh right piece,
  split it at an offset that moved back to 0 — a no-op — and so anchored after
  the whole piece. The caret edit at offset 9 deleted `🇰🇷ㅇㄹ`. Moving
  forward puts both resolutions on the same boundary. The unit test on the leaf
  helper could not see this; the document-level test the panel asked for is
  what failed.
- **Fixing the producer fixes the consumer.** `recreateFromSpan` slices span
  text at piece boundaries, and piece boundaries are where `SplitText` cut, so
  aligning the split is what keeps that decode off a lone surrogate. It cannot
  align its own window: the recreated node has to cover exactly
  `[offset, offset+length)` code units or every piece offset in the insertion
  shifts, and U+FFFD is the one decoding that preserves that length.
- **A failed remote change leaves a dirty clone.** `applyChanges` executed
  changes against `cloneRoot` and returned on error without dropping it, while
  `Update` nils it precisely to avoid exposing invalid state. `Tree.Edit`
  applies the `from` split before resolving `to`, so a mid-change failure is
  not a no-op. The clone is now dropped on any error, so the next update
  rebuilds it from the document.
- **Moving a split offset broke a caller that looked the split product up by
  the requested offset.** `isolateTextRange` probed `findFloorNode` under
  `from` and trusted a non-nil result. `findFloorNode` returns the greatest id
  `<= ` the probe, so once `SplitText` moved a mid-pair cut forward, the probe
  missed the right half and returned the LEFT piece — `Restore` and
  `Retombstone` then acted on the text *before* the range a wire-supplied span
  addressed. The fix reads the boundary back off the left piece (`Split`
  mutates the node in place, so its end IS the offset the cut used) and
  rejects a probe that does not land exactly there. A range that names no
  character boundary at all now isolates nothing rather than the wrong node.
- **Every path that executes on the clone first needs the same drop.**
  `executeUndoRedo` and `Update`'s document execute had the dirty-clone bug
  `applyChanges` was fixed for. A defer on a named return covers all of
  `executeUndoRedo`'s exits at once.
- **Validate at the point of use when the wire cannot decide.** Whether a span
  bound lands on a character boundary depends on tree state, not on the
  message, so the converter can only reject a negative offset or a
  Length/Value mismatch; the slice in `recreateFromSpan` guards its own range.

## Review round 3 (panel)

- **A guard written for one bound belongs on both.** `isolateTextRange` read
  the moved cut back off the piece for `from` and left `to` trusting its
  request. A span ending inside a surrogate pair therefore isolated a node one
  character too long — `Retombstone` deleted the emoji the span did not name.
  The closing bound now reads the boundary back the same way and skips the
  range when it is not `to`. The symmetry is the lesson: a forward-moved cut is
  a property of `SplitText`, so every caller of it has to re-read its result.
- **Put a wire check on the shared decoder, not on one caller.** The negative
  offset was rejected in `fromTreeRestoreSpans`, which covers restore spans
  only; `fromTreeNodeID` is what every tree id goes through — span ids, their
  parent/sibling anchors, and the `from`/`to` `TreePos` of every TreeEdit and
  TreeStyle. Moved it down one rung, where one check covers all of them. A
  change is persisted before it is executed, so an unexecutable position is a
  permanent per-document failure, not a rejected request.
- **A clone drop is testable through `Root()` vs `RootObject()`.** The three
  drop sites looked untestable because they need a change that applies to the
  clone and fails on the document. `Document.Root` reads the clone and
  `Document.RootObject` the document, so the invariant is one assertion; the
  triggers are a clone-only element (what `Root()` writes never becomes a
  change), a fabricated undo entry whose second operation names no parent, and
  a remote change whose second operation targets an element the receiver has
  collected. Each test was re-run with its own drop removed to confirm it
  fails.

## Review round 4 (panel)

- **A shared decoder's new rejection is half a change; the other half is the
  repair.** Moving the negative-offset check down into `fromTreeNodeID` put it
  on `ChangeInfo.ToChange`, where a rejection does not bounce a request — it
  makes the document holding that change unloadable. Round 3 moved the check
  without the `NormalizeStoredOperations` counterpart the design doc's own
  rule ("normalize what is read, reject what is accepted") demands. The
  normalizer now clamps a negative offset on every id an operation carries,
  and the rule is written down as a two-part obligation rather than an aside.
- **Parity cuts both ways.** Hardening the tree id path made the untouched
  `fromTextNodePos` the weakest decoder on the same message: nil `createdAt`
  and negative offsets both flowed straight into `RGATreeSplitNodeID`, where
  the first `Compare` dereferences them. The snapshot decoder
  (`from_bytes.go`'s `fromTextNodeID`) already refused a nil `createdAt`; the
  operation path now does too, paired with clamping in the normalizer.
- **A validity rule at the wire is a liveness rule for every producer.**
  `leftAnchorID` subtracts one from a text sibling's length, so a
  zero-length sibling produced offset `-1` — a span the server now refuses on
  every retry, wedging the pushing client's loop. Local edits cannot make an
  empty text node, but a remote peer's decoded contents can, so the producer
  has to survive one. Any new wire check needs the same sweep: what does this
  reject that our own clients can emit?

## CI round: `BenchmarkGetDocuments/with_root_presence_1000`

- **A bounded cache is not a handoff channel.** `GetMinVersionVector` loaded
  the version vectors, `Add`ed them to `vectorCache`, then read the same key
  back out and returned `ErrVersionVectorNotFound` when the read missed. The
  cache is a 16-shard LRU, so `VectorCacheSize: 1000` is ~62 slots per shard;
  the 1000-doc benchmark fans out over exactly that boundary and a concurrent
  `Add` to the same shard can evict the entry between the write and the read.
  The 10- and 100-doc cases stayed green because they never filled a shard.
  The loaded map is now kept on the stack and used directly — the cache is
  written to, never read back for the value we already hold. Every other cache
  in `mongo/client.go` already followed that shape; this was the one that
  didn't.

## Review panel round: text half of a tree-half fix

- **A contract change has two implementations, not one.** Moving a mid-pair
  cut forward was applied to `TreeNode.SplitText` *and* `TextValue.Split`, but
  only the tree's consumers were taught the new contract. `splitNode`'s
  callers still assumed the cut lands exactly where asked, so
  `RGATreeSplit.isolateRange` could take the `offset == contentLen` branch and
  hand `restore`/`retombstone` a `node.next` that is nil or belongs to another
  insertion. `isolateRange` now mirrors `Tree.isolateTextRange`: it asks
  `SplitOffset` where the cut would land, returns a nil target when that is
  not the bound the span named, and both callers skip a nil.
- **Alignment belongs to the caller that owns the ID, not to the primitive.**
  Aligning inside `TextValue.Split` broke `subValue`, whose fragment is
  registered under an ID range it does not choose: a moved cut shifted the
  window by a code unit and could ask the shortened tail for an offset past
  its end. `Split` now cuts exactly where told (clamped, so it cannot slice
  out of range) and `SplitOffset` is the alignment oracle that `splitNode`
  — the one caller that also derives an ID from the cut — applies. The two
  callers that cannot accept a moved cut get the exact window, U+FFFD and
  all, which is the trade `sliceSpanValue` already makes on the tree side.
- **Reject at the wire only what no peer can emit.** The negative-offset
  rejections added last round had no repair on the inbound path, and the only
  known producer's fix (`leftAnchorID`) is this implementation's alone — a
  peer SDK still running the old arithmetic would have every retry refused
  with nothing to revise. The wire now clamps (`clampWireOffset`), the same
  repair `NormalizeStoredOperations` makes to a change already in storage, so
  the invariant survives without the liveness cost. `from_bytes.go`'s
  `fromTextNodeID` — reachable from client-supplied Set/Add element bytes —
  was the hole this closed on the text side.
- **A skip keyed on equality has to be keyed on the write, too.**
  `UpdateMinVersionVector` skipped `updateVersionVector` whenever the pushed
  vector equalled the cached one. For an attached client that write is an
  idempotent upsert; for a detaching one it is a DELETE, and a detaching
  client pushes the vector it last pushed, so the row survived the detach and
  resurrected on the next cache miss. The skip is now conditioned on the
  client still being attached.

## Review panel round: nil tickets and the text half's missing test

- **A guard added on one side of a mirrored pair needs the mirrored test.**
  The nil-target skip in `RGATreeSplit.restore`/`retombstone` was the text
  twin of `Tree.isolateTextRange`'s, but only the tree side got tests, so the
  text side's only evidence it worked was that nothing crashed.
  `TestTextRestoreSpanBoundInsideSurrogatePair` drives a `[2, 3)` span across
  the clef's two code units through both entry points; with either guard
  reverted it panics in `SetRemovedAt`, which is the whole point.
- **Hardening one field of an untrusted message is not hardening the
  message.** `fromTextNodePos` refused a nil `createdAt` because
  `Ticket.Compare` reads `other.lamport` straight off the pointer, but every
  other ticket on the same client-supplied operation still decoded to nil via
  `fromTimeTicket`'s `(nil, nil)` — `executed_at` above all, which every
  `Execute` walks into an `After`/`ActorID` call. `fromRequiredTimeTicket`
  now refuses each ticket an operation cannot be interpreted without.
- **"The test constructs it" is not "a peer can send it."** Requiring
  `executed_at` broke two operation tests that encoded a reverse straight out
  of `Execute`. `Document.Undo` stamps the reverse before it joins a change
  (document.go:454) and JS's `toOperation` throws on an unstamped one, so that
  wire form has no producer; the tests now stamp it, which is what the path
  they claim to model actually does.

## Review panel round: the element is a ticket too

- **"Every ticket on the operation" stopped one level short of the value.**
  `fromRequiredTimeTicket` covered the operation's own tickets, but the
  element a `Set`/`Add`/`ArraySet` carries decoded its `created_at` with the
  nil-tolerant `fromTimeTicket` on all five inline branches, and the bytes
  branches only nil-checked *nested* members — never the outermost element.
  That createdAt is the element's identity: `ElementRHT` keys by
  `Ticket.Key()` and resolves by `Ticket.Compare`, both reading off the
  pointer. The previous round's test docstring already claimed this case; it
  now has the four cases (three inline, one through `BytesToObject`) that
  make the claim true.
- **A rejection needs a reason it has no `NormalizeStoredOperations`
  counterpart, written down.** Every clamp in `normalize.go` carries its
  argument for existing; the ticket rejections carried no argument for *not*
  existing, which reads the same as an oversight. They are sound because the
  accepted shape already panicked the load that decoded it and because a
  ticket cannot be repaired — invent one and every replica invents a
  different operation. The file now says so.
- **Serializing the publish does not serialize the read it publishes.** The
  version-vector loader took `vectorCacheMu` only around `vectorCache.Add`,
  so a detach landing during the Mongo `Find` found no cache entry to
  `Delete` from and the loader then published a map still naming the departed
  client — the exact pinning the detach was meant to end. `vectorCacheLoads`
  registers the load *before* the read, so the write marks it stale and the
  loader discards what it read. One uncached, conservatively low min beats a
  cached wrong one.
