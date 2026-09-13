# Undoing a container removal discards a peer's concurrent edit inside it

**Created**: 2026-09-12

Permanent replica divergence with user-visible edit loss, reachable with two
clients and no garbage collection. Pre-existing on `main`; **not** introduced by
the containment release (`f2533292`), which contains a neighbouring family.

Filed rather than fixed: it was investigated to the point of establishing that
**no fix converges without the wire format change**, which is the
identity-preserving revive work ("train 1"). The evidence for that is below, so
the question does not have to be reopened from scratch.

## Problem

    d1 and d2 share  {"c":{"a":"1"}}
    d2: c.b = "2"                   -- not yet delivered
    d1: delete c ; undo             -- d1 has never seen d2's edit
    deliver d2 -> d1, then d1 -> d2

    d1 = {"c":{"a":"1","b":"2"}}   Live{4,168}
    d2 = {"c":{"a":"1"}}           Live{2,120}

Unchanged after `GarbageCollect` on both with a max version vector. The peer's
own edit is the one that vanishes, on the peer.

The order matters and only one order diverges: delivering the peer's edit
*before* the undo converges. That asymmetry is the defect, not a mitigation.

## Mechanism

The reverse of a `Remove` is a `Set` carrying `value.DeepCopy()`, taken at
removal time (`operations/remove.go:112-126`). Applying it is a whole-subtree
install that re-points every createdAt-keyed index at the copy:

- `operations/set.go:93` — `value, err := o.value.DeepCopy()`
- `operations/set.go:103` — `obj.SetWithExecutedAt(...)`, which at
  `crdt/element_rht.go:142` does `rht.nodeMapByCreatedAt[v.CreatedAt().Key()] = newNode`
- `operations/set.go:134` — `root.RegisterElement(value)`, which at
  `crdt/root.go:141-152` writes `r.elementMap[...]` for the element **and every
  descendant, each under its original createdAt**

The read side that forks is `operations/set.go:57` —
`parent := root.FindByCreatedAt(o.parentCreatedAt)`.

So one `createdAt` names two different element instances at two different points
in the document's life, and an operation addressed at that `createdAt` lands on
whichever instance owns it **at apply time**, which delivery order decides. A
whole-subtree install does not commute with an operation addressed *inside* the
subtree.

There is no reconvergence path. The losing side's original subtree is orphaned
but stays in `Root.elementMap`; `Set.Execute`'s `UnregisterRemovedElementPair`
(`set.go:134`) retires its collection entry, so `GarbageLen` is 0 and no pass
will ever visit it. Nothing re-examines an already-applied operation.

## Why re-ticketing does not fix it

The obvious cheap fix is to give the restored subtree a fresh `createdAt`, the
way `executeUndoRedo` already does for the array path
(`document.go:449-451`, `addOp.Value().SetCreatedAt(ticket)` plus
`history.ReconcileCreatedAt`).

**The array path already re-tickets and still diverges**, because descendants
are never re-ticketed. The peer's operation is addressed at a *descendant*, and
identity reuse anywhere inside the installed subtree is sufficient. This is
pinned by `TestRestoredSubtreeDropsConcurrentDescendantEdit`, which shows the
object and array parents failing the same way for the same reason — so
`TestUndoneArrayRemoveSurvivesCollection` passing does **not** mean the array
path is safe here.

## Why "revive the tombstone instead" does not fix it either

The replica performing the undo usually still has the tombstone, so it could
revive in place rather than install a copy. It cannot be made to converge with
local state alone:

- a replica that collected between receiving the removal and receiving the undo
  has purged the tombstone;
- a replica that joined by loading a snapshot taken after that collection never
  had it.

Whether a replica *can* revive is therefore a function of its local collection
timing, not of the change log — so "revive when the tombstone is there" produces
different documents on different replicas from the same log. Measured by
`TestRestoreTombstoneIsNotReliablyPresent`, which passes on `main` today.

That is the whole argument for putting the decision **on the wire**: every
replica must take the same branch, which means the branch cannot be chosen from
local state.

## Reproduction

`pkg/document/restore_divergence_test.go` in the investigation worktree holds
three tests. Two assert convergence and therefore **fail on `main`**; they are
the reproduction, not a regression suite, and must not be committed to CI until
the fix lands:

- `TestRestoredContainerDropsConcurrentPeerEdit` — both delivery orders
- `TestRestoredSubtreeDropsConcurrentDescendantEdit` — object and array parents

The third, `TestRestoreTombstoneIsNotReliablyPresent`, passes today and is a
characterization test of the precondition; it is safe to land as-is.

## Tasks

- [x] ~~Land `TestRestoreTombstoneIsNotReliablyPresent` on its own~~ — landed in
      `pkg/document/restore_precondition_test.go`. It is green and it pins the
      argument that the branch cannot be chosen locally
- [ ] Adopt the two failing tests as **acceptance tests for train 1**, not as
      regressions. They are what "train 1 worked" means for this defect
- [ ] Decide whether this warrants an advisory before train 1 ships. It is
      silent, it loses a user's own edit, and there is no operator-visible
      signal — `GarbageLen` is 0 and `docSize` closes
- [ ] Re-check after train 1's content-authority rule is implemented. The rule
      chosen for train 1 (tombstone-authoritative revive, with the carried
      payload used only when the tombstone is gone) is what closes this; confirm
      against these two tests rather than against the design document

## See Also

- `docs/tasks/active/20260816-remote-redo-replica-divergence-todo.md` — the
  collecting filing for undo/redo defects; this one is its content-side twin
