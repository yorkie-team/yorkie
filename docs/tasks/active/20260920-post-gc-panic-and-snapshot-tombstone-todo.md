# Post-GC style panic and the snapshot's lost text attribute tombstone

**Created**: 2026-09-20

Two independent defects, both found while reviewing #2005, both blocking nothing
else and both cheap to fix. They ship together because neither is large enough to
justify its own review cycle, and they are kept in separate commits so either can
be split out with a single cherry-pick.

## Context

Five issues came out of the #1998 / #2000 / #2005 size-ledger work. They sort into
four groups by nature, not by area:

| group | issue | nature |
|---|---|---|
| A | #2008 | crash — the process dies |
| B | #2006 | convergence — removed formatting comes back and never reconciles |
| C | #2007 then #2002 (text half) | ledger — size leaks, GC cannot collect |
| D | #2003 | representation — the two SDKs store attribute values differently |

This task covers groups A and B, the two that need no other issue landed first.

Group C is strictly ordered: #2007 must land before #2002's text half, because two
attempts at the reverse order in #2005 both failed, and one of them drove
`docSize.GC` negative — which is worse than the leak it fixed, since `MaxSizeLimit`
reads `Live + GC`. Group D is a protocol decision before it is a patch.

Fixing #2006 first also has a tooling payoff for group C: it is why #2002's text
tests have to rebuild in memory rather than through the converter. Once the
converter can carry a text attribute tombstone, converter round-trip tests become
available as the regression harness for #2007 and #2002.

## Goals

- #2008: a `Tree.Style` over a collected subtree returns an error instead of
  panicking. Scope is the nil guard only (item 1a).
- #2006: a text attribute tombstone survives a snapshot round trip, so a replica
  served from a snapshot renders the same content as one that replayed the changes.
- Establish whether #2008's panic is reachable on the server's remote-apply path.
  There is no `recover()` in the RPC path, so if it is reachable, one client can
  crash the process and the guard is an urgent standalone patch.
- Scope the follow-up for #2008's root cause (item 1b) without fixing it here.

## Non-Goals

- **#2008 item 1b** — that `Undo` reverses an operation whose target is already
  gone. It is a design judgement about the undo stack's documented limitation, and
  holding the crash fix for it would be the wrong trade. Filed as follow-up.
- **The JS SDK half of #2006.** `converter.ts`'s text-node path has the same
  omission. It is a different repository, so it cannot be in this PR; it needs a
  paired PR against `yorkie-js-sdk`.
- **#2007, #2002, #2003.** Ordered after this work for the reasons above.
- **Recovering documents already corrupted in the wild.** A snapshot written
  without `isRemoved` simply does not carry the information; honouring the flag on
  decode cannot recover what was never written.

## Plan

- [x] Sync to `main` (`75e1ae61`, #2005 included) — the reports were written at
      `a23ba9b8`, one commit earlier
- [x] #2008 — reproduce the panic as a failing test in `pkg/document`
- [x] #2008 — guard the `toTreePos` ascent loop; prove Red to Green
- [x] #2006 — reproduce the lost tombstone as a failing converter round-trip test
- [x] #2006 — carry `isRemoved` through `toTextNodes` and `fromTextNode`; prove
      Red to Green
- [x] Verify each fix adversarially: revert only the production files, confirm the
      test fails without them
- [x] Answer the server-reachability question for #2008
- [x] Scope the 1b follow-up
- [x] `go test ./...` green, `make lint` green
- [x] Self review over the full branch diff
- [ ] Open the PR, one commit per issue
- [ ] File the 1b follow-up issue (restore under a tombstoned parent)
- [ ] File the paired `yorkie-js-sdk` PR for #2006's JS half

## Review

Both fixes landed and were verified by reverting the production files while
keeping the test, so neither test is decorative. `go test ./...` is green across
81 packages, `make lint` reports 0 issues, `gofmt` and `go vet` are clean.

### #2008 — server reachability, answered

**Not reachable on the server today**, and the earlier reasoning that said
otherwise was wrong on two counts.

The panic *is* reachable through the server's apply function when it is driven
directly — two shapes reproduce it, including a single push-only client, which
needs no second actor and no undo primitive on the server. What saves the real
server is that every apply path interposes an `InternalDocument.DeepCopy()`
between collection and the next apply (`server/packs/snapshot.go:77` and `:157`),
and `NewTree` rebuilds `NodeMapByID` by walking the live index, so a node
detached by `Purge` is never re-registered. Inserting one `DeepCopy` into the
panicking probe turns it into a pass; that is the whole mechanism.

**That protection is incidental.** Those copies exist for cache-aliasing reasons,
nothing documents them as a safety property, and removing either — an
obvious-looking allocation win on a hot path — makes the panic server-reachable
the same day. The commit body records this so the next person does not have to
rediscover it.

The second correction: "there is no `recover()` in the server, so a reachable
panic kills the process" was wrong. `recover()` genuinely appears only in test
files, but the RPC server is a plain `http.Server` under `h2c`
(`server/rpc/server.go:102-108`), and net/http recovers handler panics per
connection while x/net/http2 recovers per stream. A reachable panic would break
one stream, not the process. So this is a normal PR commit, not a P0 hotfix.

### #2008 — the fix is necessary, not sufficient

Two separate things must be said when closing the issue.

First, the reproduction still panics after this change. `json.(*Tree).Style`
does `panic(err)` on any error from the crdt layer, and `Document.Update` does
not recover, so an unprotected caller still dies — with
`*fmt.wrapError: "least alive ancestor of …: node not found"` instead of a nil
dereference. Only the *fault* is gone. Changing the json package's
panic-on-error convention is a public API break and is out of scope.

Second, the document is still inconsistent afterwards. The guard stops the
crash; it does not remove the live, `NodeMapByID`-registered node dangling off a
detached subtree. Closing #2008 as "fixed" without saying so would bury the real
defect.

### #2008 item 1b — rescoped, and it is not what the report assumed

The report's hypothesis is right about the trigger and one level too high about
the cause. The split-and-remove producing no reverse operation is confirmed
(`operations/tree_edit.go:457-474`, gated on `isPureSplit`) and is the documented,
deliberate limitation behaving as designed — the undo stack never mis-orders
anything, `PushUndo` is simply never called.

The inconsistent document comes from somewhere else: `Tree.recreateFromSpan`
(`pkg/document/crdt/tree.go:1265-1268`) checks the restore target's parent for
*identity* but never for *liveness*, so it resurrects a live text node under an
already-tombstoned `<span>`. The next `Purge` nils the span's own `Index.Parent`
and leaves that live child dangling and still registered. The invariant broken is
"every node in `NodeMapByID` is reachable from the root".

The decisive evidence is an independence result: with no split anywhere and a
perfectly aligned top-of-stack undo, a peer removing the enclosing span produces
the identical orphan. So 1b is a **third distinct defect** — it must not be
folded into this PR, and it must not be closed against
`20260816-tree-split-edit-loses-undo-entry-todo.md`, whose proposed fix does not
touch `recreateFromSpan` at all.

The one-line `|| parent.IsRemoved()` fix passes the entire unit suite, which is
exactly why it should not be merged yet: B1's documented justification is that
the parent is *purged*, which every replica resolves identically, whereas
*tombstoned* is replica-local and time-varying. Extending it may trade a crash
for a divergence. The first deliverable on the follow-up is the two-replica
ordering test that settles it — not the patch. `tree.ts:2718-2729` has the same
omission, so a paired JS PR is needed whichever way it goes.

### #2006 — parity is exact, and the fix is forward-only

The rebuilt root now matches the live document on all four measures, not just
the content one the issue leads with: `Marshal()`, `GarbageLen()`,
`DocSize().GC` and `DocSize().Live`. The tombstone is genuinely collectable
afterwards, not merely counted — collecting the rebuilt root purges 1 and leaves
`gcLen=0`. A mixed node and a three-way split both converge exactly.

Old snapshots decode exactly as before (absent field reads as false), so
compatibility is clean, but **documents already corrupted cannot be healed** —
the distinguishing bit was never written, and the surviving attribute carries the
removal's own ticket, so it keeps winning LWW. Repairing those needs a separate
data pass.

`crdt.NewRoot` does register a GC pair for a text attribute tombstone
(`TextValue.GCPairs` → `Text.GCPairs` → the `*Text` case in the `Descendants`
walk), which is why the accounting comes out exact rather than half-fixed.

### Follow-ups this surfaced

1. **1b as its own issue** — the restore/liveness defect above.
2. **The `yorkie-js-sdk` half of #2006.** `converter.ts` drops the flag in *both*
   directions, so a JS replica hydrating the now-correct server snapshot still
   renders the removed formatting. The issue is not resolved end to end until
   that lands.
3. **A nil-ticket guard for removed attributes.** Honouring the flag means a
   decoded attribute can become a GC pair, and `keyOf` builds the key from the
   ticket with no nil check, so a removed attribute with a missing `UpdatedAt`
   panics in `NewRoot`. No Yorkie encoder produces that shape, and the identical
   panic is already reachable through the untouched tree path, so it is a
   pre-existing fragility class rather than one this PR introduces — but
   client-supplied bytes do reach `BytesToObject`, so a poisoned element could be
   persisted and crash every later snapshot load. One rejection in both `fromRHT`
   and `fromTextNode` closes the class.
4. **`Tree.Purge` dereferences `node.Index.Parent` unguarded**
   (`crdt/tree.go:1021`) while its sibling `PurgeBarrierAt` guards that exact
   field. Not shown reachable; the asymmetry is worth closing.
5. **Defense in depth for the server.** Nothing in `server/` recovers on its own;
   it rides entirely on net/http's per-stream recovery. A `connect.WithRecover`
   interceptor and a recover in `background.Go` are worth having on their own
   merits.
6. **`Retombstone`**, the redo counterpart of `Restore`, was not checked for the
   same liveness hole.

## Verification

Per-fix Red/Green is the floor, not the ceiling. Both changes claim to make a
document's rendered content or size reproducible from its own content, which is the
invariant the whole #1998 line of work rests on — so the bar is the one #2005 used:
snapshot round-trip divergence measured over many iterations, not a single green
test.

For #2006 specifically, `Marshal()` equality is the content check; `GarbageLen()`
and `DocSize().GC` equality is the accounting check. These can come apart —
`crdt.NewRoot` may not register a GC pair for a text attribute tombstone the way it
does for a tree one. Both are measured and reported separately.

## See Also

- `docs/tasks/active/20260919-tree-split-live-size-dropped-todo.md` — the #1998
  work whose Non-Goals recorded #2002 and #2003
- `docs/tasks/active/20260919-split-copies-attr-tombstone-id-todo.md` — #2005, the
  tree half of #2002; both defects here were found while reviewing it
