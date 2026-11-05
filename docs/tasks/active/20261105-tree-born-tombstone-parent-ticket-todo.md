**Created**: 2026-11-05

# Born-tombstoned tree node is purged while the inserter builds on it

## Problem

Issue #2327. When client A inserts into a paragraph that client B has
concurrently removed, B's `Tree.Edit` creates the inserted node already
tombstoned ("born tombstoned"), stamping it with **A's own edit ticket**
(`editedAt`):

```go
// pkg/document/crdt/tree.go, Phase 8
if fromParent.IsRemoved() {
    node.Value.remove(editedAt)
    pairs = append(pairs, GCPair{Parent: t, Child: node.Value})
}
```

`minVersionVector` covers A's ticket the moment the insert is delivered, so
B purges the node on the very next GC pass. A has not pulled the removal yet
and keeps anchoring its next keystrokes to that node, so A's next change
refers to a node B no longer has:

```
ErrChangeApplyFailed: ... cannot find node of CRDTTreePos(...)
```

The server redelivers the same pack forever, so the two clients stay stuck.
Reported against JS SDK 0.7.25 + server v0.7.25; reproduces on Go `main`.

The asymmetry is the tell. On A's replica the node is never tombstoned at
all: when B's removal arrives, `canDelete` declines because the removal's
version vector does not know the node's creation. So A purges it only when
the parent subtree goes, which is gated on the PARENT's `removedAt`. B gated
it on A's ticket. The two replicas purged the same node on different vectors.

`recreateFromSpan` in the same file already faced this and already stamps the
parent's `removedAt`, with a comment arguing exactly this point. Phase 8 was
the one spot that did not.

## Approach

Stamp the born-tombstoned node with `fromParent.RemovedAt()` instead of
`editedAt`, matching `recreateFromSpan`. No vector covers that ticket until
every replica, the inserter included, has pulled the removal.

The stamp stays overwritable by the eventual LWW winner — `TreeNode.remove`
overwrites on a strictly later ticket — so a later concurrent removal still
wins, which is the property `recreateFromSpan` relies on too.

Scope: Go only. The JS SDK has the same line
(`packages/sdk/src/document/crdt/tree.ts`) and needs the same fix in its own
repo; the two are independent because the ticket is local bookkeeping, not
wire format — neither `removedAt` choice is transmitted, each replica derives
it when it applies the insert.

## Steps

- [x] Reproduce with a two-replica unit test that keeps the inserter behind.
- [x] Change the Phase 8 stamp to the parent's `removedAt`.
- [x] Assert the three properties: not purged while the inserter is behind,
      the inserter's next change still applies, and both replicas do purge it
      once the removal is known to all.
- [x] Document the rule in `docs/design/garbage-collection.md`.
- [x] `go test ./...` and `make lint`.

## Notes

- `exchangeInOrder` (the existing tree-test helper) cannot express "A is
  behind": it acks every replica's pack whether or not anyone received it, so
  B's undelivered removal is lost. The test adds `deliverTo`, a one-way
  delivery that acks only the sender.
- docSize accounting is untouched. The branch already registered a plain
  `GCPair` and already skipped the `diff.Add` the live branch does; only the
  ticket on the node changed, and `DataSize` does not read `removedAt`'s
  value, only whether it is set.
