# Collection changes where a later insert lands

**Created**: 2026-09-12

Permanent replica divergence, and separately hard apply failures, caused by
garbage collection. Pre-existing on `main`; not introduced by the containment
release (`f2533292`). Present in **all three** RGA-shaped structures — `Array`,
`Text` and `Tree`.

Three attempts were made. Attempt 2 **ships in this PR as a partial fix** — it
closes the stopping-point half in all three structures and leaves the anchor
half open. Attempts 1 and 3 were refuted by measurement, and this filing records
what they established so a fourth attempt does not repeat them.

## Problem

RGA decides where an insert lands by walking forward from its anchor, skipping
nodes whose positioning ticket is newer than the incoming operation. The walk
reads the nodes **currently linked into the list, tombstones included**:

    pkg/document/crdt/rga_tree_list.go:526   node.next.PositionedAt().After(executedAt)   (Array)
    pkg/document/crdt/rga_tree_split.go:437  node.next.createdAt().After(updatedAt)       (Text)
    pkg/document/crdt/tree.go:2998           !next.id.CreatedAt.After(editedAt)           (Tree)

Collection unlinks exactly those nodes (`RGATreeList.release`, and the
equivalents). So **collection mutates the input to the insertion rule**, and the
insertion point stops being a function of state the replicas agree on — which is
what RGA convergence requires.

Two replicas that differ *only* in whether collection has run therefore order the
same later insert differently, and nothing reconverges them.

The general statement: **tombstones are load-bearing for the forward skip.** A
position slot abandoned by `MoveAfter` is simply another species of tombstone
that inherits this; the defect is not move-specific and a plain `Delete`
reproduces it.

## Measurements

A fuzz harness with a push/pull model faithful to `pushpull.go` — each client's
row stored from its request's version vector at push time, own changes filtered
out of the pull, `minVV` the element-wise minimum over rows, and collection run
only by `Document.ApplyChangePack` with the vector that pull delivered. No
`helper.MaxVersionVector` anywhere.

On `main`:

| workload | result |
|---|---|
| 300 seeds, all operations, collection **on** | 43 diverged or errored |
| 300 seeds, all operations, collection **off** | 0 — converges |
| 1000 seeds, insert + delete only | 4 diverged, 75 `ErrChildNotFound` |
| 1000 seeds, all operations | 109 diverged, 26 `ErrChildNotFound` |

The collection-off control is the load-bearing one: the same seeds converge when
nothing is collected, so collection is the sole cause.

Note the second failure mode. Beyond divergence, the walk can be handed an
anchor that collection removed, and the operation then fails to apply —
`ErrChildNotFound` in the array, "offset should be less than or equal to length"
in text. An operation that cannot apply is worse than one that applies
differently: the server rebuilds documents and snapshots by replaying the change
log, so a stored change that fails to apply makes the document unloadable.

## Attempt 1 — refuted

Barrier on the node being purged: an element a move rehomed may not be purged on
its `removedAt` alone. Guarded the `gcElementPairMap` loop in
`Root.GarbageCollect` and left the `gcNodePairMap` loop, on the argument that a
move's gate (the move ticket) is always causally after its barrier (the
element's original insert ticket).

**True only for the first move.** `MoveAfter` abandons the node the *first* move
created, so a second move's dead slot has barrier `m1` and gate `m2`. Concurrent
moves by different actors leave those unordered, and a legitimate `minVV` can
cover `m2` while `m1` is in flight. Refuted independently by two reviewers with
the same counterexample.

## Attempt 2 — ships here, as a partial fix

Reframed the barrier as a property of the **successor**, not of the node being
purged:

> A purge may unlink a node only if the node that would become the forward
> skip's new stopping point is itself causally stable.

This is the better framing — it needs no case analysis about what a particular
slot's barrier is, which is exactly what made attempt 1 wrong, and one rule
covers both purge loops and all three structures.

Measured against `main` on the same fuzz:

| | main | attempt 2 |
|---|---|---|
| 300 seeds, collection on | 43 | **33** |
| 1000 seeds, insert+delete: diverged | 4 | **0** |
| 1000 seeds, insert+delete: `ErrChildNotFound` | 75 | **40** |
| 1000 seeds, all ops: diverged | 109 | **84** |
| 1000 seeds, all ops: `ErrChildNotFound` | 26 | **18** |

Better in every category, and it eliminates insert/delete divergence entirely.
Retention cost measured at +5.2% mean collected pairs on a move-heavy workload
and byte-identical to `main` on ordinary ones; it drains within one sync round
and is charged to `DocSize.Total`, so `MaxSizeLimit` accounts for it.

**It does not close the bug, and it is not presented as closing it.** Purging a
tombstone destroys two things and the successor barrier addresses one:

1. the skip's stopping point — closed by attempt 2;
2. the **anchor** that an operation concurrent with the removal still
   references — untouched, because the successor can be perfectly stable while
   an in-flight operation points at the node being removed.


## Attempt 3 — four framings, all reached the bar, none shippable

Four framings were built and measured against the same fuzz. **All four reached
0 of 300.** None closed the defect, and the reason matters more than the
framings: **the acceptance criterion above was the wrong bar.** It is array-only,
string-elements-only, no-undo, and client-side-collection-only, and every framing
fails outside that box.

| | framing | fuzz | why it is excluded |
|---|---|---|---|
| A | anchor barrier (attempt 2 + three gates) | 0/300 | 48 B per moved element retained in `DocSize.GC` indefinitely; Text and Tree anchor half open by the author's own admission; its load-bearing argument about server append ordering was refuted |
| B | origin-carrying insertion rule (YATA shape) | 0/300 | two new proto fields and a flag day; array-only, and **not** a superset of attempt 2 — attempt 2's Text and Tree tests fail under it; legacy snapshots permanently sticky |
| C | retain the position node, collect only content | 0/300 | author reported `viable: false`. `docSize = base + 48·edits` with no asymptote, 111× snapshot at 10k edits, and it **refuses assignment 5 of 30** under `MaxSizeLimit` |
| D | reference discipline (fix the anchor producers) | 0/300 | refuted twice independently, on correctness and on cost |

D was the right candidate on paper — 323 production lines, no schema change, zero
upstream tests modified — and it is the one most thoroughly destroyed.

### The measured ceilings — this is the real result

Every number from the same faithful `advServer` harness, with paired attribution
where the control is dirty: run each seed twice, differing only in the vector
handed to `GarbageCollect`, and count only seeds where the control **passes** and
collection-on **fails**. `onlyControlFails` was 0 in every cell.

**Array, no undo, string elements — solved.** A, B and D all reach 0, and D was
then attacked with 8,000 nested-container runs and 12,000 server-replay runs it
was never tuned against — both of which fail on `main` at 155-402 and 143-277 per
1000 — with **zero** failures.

**Array + undo/redo — open.** 2000 seeds per cell:

| clients | main | framing D |
|---|---|---|
| 2 | 295 (childNotFound 214, diverged 81) | 66 |
| 3 | 440 (320 / 120) | 107 |
| 4 | 751 (558 / 193) | 198 |
| 5 | 979 (723 / 256) | 327 |

D cuts it about 70% and removes the silent-divergence class entirely, but does
not reach 0. Four failing call sites, all `child not found`: `MoveAfter`,
`insertAfter`, `FindPrevCreatedAt`, `DeleteByCreatedAt`. The anchors the history
stack captures when a forward op is issued, and executes arbitrarily later, are
a producer site no framing enumerated.

**Text — open, one mechanism.** 2000 seeds per cell, 2/3/4 clients: main
269/291/300 → D 235/233/243. Every failure is `the node of the given id should
be found`.

**Tree — open, one mechanism.** main 207/252/297 → D 177/199/240. Every failure
is `node not found`.

So attempt 2's successor barrier — the only thing A and D do for Text and Tree —
buys 15-20% there and nothing more. The anchor half in those two structures is
now **measured** rather than admitted, and it is one mechanism each, the same one.

### What killed D on cost, measured here

D's own retention fixture re-moves a handful of elements by index and retains
exactly 7 slots at every array size, so its reported cost is independent of the
array. On a **drag-reorder** — each element moved once, the canonical move
workload — retention is n−1:

| n=200, collect after each move | main | framing D |
|---|---|---|
| `garbageLen` | 0 | 199 |
| `DocSize` total | 6472 | 16024 (+148%) |
| `DocSize.GC` meta | 0 | 9552 — twice the live metadata |

`DocSize.Total` feeds `MaxSizeLimit`, so this is enforced and user-visible: a
20-element array under a 1000-byte limit accepts all 19 drags on `main` and is
**refused at move 8** under D. Idle `GarbageCollect` also regresses about
5,200× at n=1000, which no existing test covers.

### Framings now closed by evidence

- **Constrain collection with a better predicate** (attempts 1, 2, 3A) — the
  predicate is not one ticket. It is at least three structural facts, and each
  one found adds retention. A reaches the array bar and pays 48 B per moved
  element forever.
- **Change the insertion rule** (3B) — costs a wire change and a flag day, and
  did not even generalise to Text and Tree, which is where the cheap win was
  supposed to be.
- **Retain the structure** (3C) — unbounded by measurement, and its own author
  declared it unshippable.

What is left is the anchor half in `Text` and `Tree`, plus the history stack's
captured anchors — and the only framing that removes the dependency rather than
constraining collection needs a wire change. **That places this defect with the
identity-preserving revive work rather than as a standalone fix**, which is where
this filing now recommends it goes.

### Artifacts

Four diffs, none proposed for merge. `wip/rga-successor-barrier` carries attempt
2. Attempt 3's four diffs were produced in session worktrees; D is the smallest
and the best documented, and a fourth attempt should start from the ceilings
table above rather than from any of the diffs.

## Tasks

- [x] ~~Decide whether attempt 2 ships on its own~~ — **yes, it ships here.**
      This reverses an earlier decision in this file, and the earlier reasoning
      was wrong in a way worth recording: it argued that three framings beat
      attempt 2 on the array bar and none of them is shippable, therefore
      "shipping the weakest of them buys nothing". If every alternative is
      unshippable then the comparison against them carries no information, and
      attempt 2 has to be judged on its own record. That record is every fuzz
      category improved with none regressed, one class of silent divergence
      eliminated outright, and a cost that no workload has been found to
      distinguish from `main` — including the drag-reorder that disqualified
      framing D. "Not a complete fix" is not "not worth shipping"
- [ ] **Carry the anchor half into the wire-format work.** That is attempt 3's
      conclusion and the reason this is not a standalone fix
- [ ] Close the anchor mechanism, or establish that it is unreachable through
      the real push/pull path. Attempt 2's author argued unreachability from
      reading `pushpull.go` and `document.go` and was explicit that it was an
      argument and not a measurement; the fuzz above reaches it
- [x] ~~Decide about `findNextBeforeExecutedAt` itself~~ — built in attempt 3
      (framing B). Two new proto fields, a flag day, legacy snapshots
      permanently sticky, and it did not generalise to Text or Tree. It remains
      the only direction that removes the dependency, which is why the
      conclusion is to take it inside the wire-format work rather than alone
- [ ] Whatever is chosen, the JS SDK needs the identical decision — the same
      three skips exist there
- [ ] **Replace the acceptance criterion.** The array/no-undo/strings fuzz is
      met by three independent framings and is therefore no longer
      discriminating. A real bar needs undo/redo in the op mix, `Text` and
      `Tree` harnesses, and paired attribution so a dirty control does not hide
      the signal — all three exist in the attempt 3 transcripts

## Reproduction and artifacts

`pkg/document/gc_rga_fuzz_test.go` holds the harness, behind the `rgafuzz` build
tag because it is expected to fail — that is the point of it. It still fails with
attempt 2 applied, at 33 of 300 rather than 43, and the remaining failures are
the anchor half. `go vet -tags rgafuzz ./...` runs in CI so a refactor cannot rot
it without running it.

Attempt 2 ships here: the successor-barrier change plus four targeted regression
tests — array remove+move, concurrent moves with a server-computed `minVV`, the
same defect in `Text`, the same defect in `Tree` — each verified red on `main`
and green with the fix. `gc_rga_barrier_cost_test.go` pins the cost shape rather
than a golden number, because a misleading cost fixture is exactly what let
framing D look affordable.

A fourth attempt should start from the ceilings table above, not from any of the
attempt 3 diffs. Those are preserved, none proposed for merge:
`wip/rga-anchor-barrier` (framing A, and its commit message records the three
structural gates it had to discover), `wip/rga-origin-insertion` (framing B, the
only direction that removes the dependency rather than constraining collection),
`wip/rga-reference-discipline` (framing D).

## See Also

- `docs/tasks/active/20260912-undo-discards-concurrent-peer-edit-todo.md` — the
  other divergence found in the same investigation, on the content side
