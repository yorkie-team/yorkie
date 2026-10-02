# Lessons — reject mid-surrogate-pair indexes

**Created**: 2026-10-02

## Notes

- `index.Tree.FindTreePos` hands back a text node plus a UTF-16 *relative*
  offset, which is exactly the number `SplitText` would slice at. Validating
  there means the check and the split read the same number, instead of the
  check re-deriving one from the absolute index.
- At a boundary between two adjacent text nodes, `FindTreePos` and
  `splay.Tree.FindForText` both resolve to the left node at `offset == len`.
  That is never mid-pair, which is the right answer: if an old operation
  already split a pair across two nodes, editing at the seam splits nothing,
  so there is nothing new to reject.
- `crdt.Tree.FindPos` and `RGATreeSplit.createRange` are *not* local-only —
  `operations/tree_edit.go` uses `FindPos` on the reverse/undo path. The
  validation had to go in the `json` layer rather than in those, or undo of a
  pre-existing mid-pair edit would start panicking.

- The `json` layer reports caller mistakes by panicking, and `Document.Update`
  only discarded the clone on the *error* return. A guard that panics out of
  the updater therefore left every mutation the same updater had already made
  in `d.cloneRoot`, where `Document.Root` serves it — the root never took them,
  so `Root` handed back a state that exists on no replica. `Update` now
  recovers, invalidates the clone and re-panics; the defer is registered after
  `d.mu`'s unlock so it still runs under the lock.
- Validation must be exercised on a *split* text node, not just a freshly
  created one: both wrappers resolve the index to a per-node offset, so a
  single-node fixture cannot tell a correct offset resolution from one that
  only ever looks at the first node.

## Review round (panel)

Blocking finding across all three lenses: the new panic escaped `Update`
without `invalidateClone`. Fixed in `pkg/document/document.go`; the
multi-node and dirty-clone gaps are covered by three new tests in
`pkg/document/mid_surrogate_index_test.go`.

## Review round 2 (panel, blast radius)

Blocking finding: the "a mutation the root never took must not survive in the
clone" contract was enforced only inside `Update`. `Document.Root` hands out
the *same* mutating proxies over the same clone with a throwaway context, so
an edit made through them outside an updater diverges the clone with nothing
to notice — complete or panicking, both.

Two fixes were considered and rejected before the one that landed:

- Invalidate eagerly in `root()`. Correct, but it charges a full
  `root.DeepCopy()` to every read-then-write cycle, which for a client that
  renders after each edit is a per-keystroke copy of the whole document.
- Record the handed-out contexts and check them in `ensureClone`. Misses a
  mutation that panics before it pushes its operation, and the slice grows
  without bound until the clone is rebuilt.

What landed instead: `change.Context.OnMutate`, fired from `IssueTimeTicket`
and `Push`, with `root()` registering `invalidateClone`. Every CRDT mutation
needs a ticket and issues it immediately before touching the root, so the
callback covers the half-applied panic too, while a view that is only read —
which is all `Root` is for — never fires it and pays nothing.

## Self review

Not run: this branch was produced by the autonomous issue-to-PR agent, which
is granted no tool that can dispatch the reviewer subagent. The round is
skipped, not clean — CI, `@claude review` and a human reviewer are the
review for this change.

## Review round 3 (panel, correctness + blast radius)

Blocking finding (correctness): `invalidateClone` is reachable from the
`OnMutate` callback with no lock — the view's writer runs on its own
goroutine — while `ensureClone` and `Update` write the same `cloneStale`
under `d.mu`. A plain `bool` there is a write/write race, and the harm is
not the "one rebuild deferred" the comment claimed: with no happens-before
edge to the locked readers, the store can stay invisible to all of them, so
the dirtied clone keeps being served. `cloneStale` is now an `atomic.Bool`.

Worth recording why an overwritten store is still harmless once the race is
gone: the only store of `false` is the one `ensureClone` makes right after
`DeepCopy`ing the root, so whatever clone replaces the dirtied one is clean
by construction. That is what makes "an unlocked caller may set, only a
locked one may clear" a sound rule rather than a hopeful one.

No test was added. A concurrency test would have to mutate the clone through
a `Root` view while an updater mutates the same clone, which is a genuine
race in the CRDT structures themselves and would trip `-race` whatever this
flag's type is. The fix is a type change the compiler enforces;
`go test -race ./pkg/document/...` stays green.

Non-blocking, left open (blast radius): the `OnMutate` hook covers the json
proxy methods, but every proxy embeds its CRDT node as an exported field
(`json.Text`'s `*crdt.Text`, and the same in `tree.go`, `object.go`,
`array.go`, `counter.go`), so a caller that reaches through the embedded
value mutates the clone without issuing a ticket. That escape predates this
branch — `Document.root` handed out the identical proxies over the identical
clone before the hook existed, and `Update` hands them out still — and
closing it means unexporting those fields, a breaking change to the public
`json` API far wider than this branch. Recorded as a known limitation; a
rebuttal was filed rather than a fix.
