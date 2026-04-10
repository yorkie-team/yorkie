**Created**: 2026-04-10

# Verify `merged*` Runtime-Only Fields Across Snapshot

**Related**: `20260406-concurrent-merge-split-todo.md`,
`docs/design/concurrent-merge-split.md`

## Context

`TreeNode` accumulated 4 fields during concurrent merge/split convergence
fixes: `mergedInto`, `mergedChildIDs`, `mergedFrom`, `mergedAt`. All are
marked as "runtime-only" and are **not** serialized in `api/converter`.

Problem: if a replica loads the document from a server-built snapshot,
these fields are empty. Any subsequent concurrent op that depends on
merge redirection (Fix 3), merge-move propagation (Fix 5), or split-skip
(Fix 8) may diverge on that replica vs. replicas that ran the merge op
directly.

The design doc claims "no protocol changes", but never proves that
runtime-only storage is safe. This task verifies the claim before doing
any field consolidation.

## Plan

- [x] Add unit-level test at the `api/converter` layer that roundtrips
      the document state through `SnapshotToBytes` between a merge and a
      concurrent insert.
- [x] Run the test; confirm runtime-only assumption is WRONG.
- [x] Add `merged_from` optional field to `TreeNode` in `resources.proto`
      and run `make proto`.
- [x] Export `TreeNode.mergedFrom` to `MergedFrom` in `crdt/tree.go`.
- [x] Write `MergedFrom` in `to_bytes.go`; read it in `from_pb.go`.
- [x] Add `Tree.rebuildMergeState` post-load pass in `crdt/tree.go` to
      derive `mergedInto`, `mergedChildIDs`, `mergedAt` from the
      persisted `MergedFrom` + existing tree structure.
- [x] Un-skip `TestConverter/tree_merge-and-insert_convergence_across_snapshot`.
- [x] Add sibling `tree_merge-and-merge_convergence_across_snapshot` test
      for Fix 4 coverage.
- [x] Update `docs/design/concurrent-merge-split.md` Fix 4 / Fix 8
      descriptions to reflect the snapshot encoding change.
- [x] Run `go test ./...` full suite — all green.

## Result: BUG CONFIRMED AND FIXED

The diagnostic test failed on the first run (as expected), proving the
runtime-only assumption was wrong:

```text
docA (ran merge directly, has merged* fields)        -> <root><p>acb</p></root>
docB (loaded from snapshot, merged* stripped)        -> <root><p>ab</p></root>
```

`docC`'s insert was **silently dropped** on docB because
`FindTreeNodesWithSplitText` could not find `mergedInto` on the
tombstoned parent and had no fallback. This was a latent data-loss bug
for any replica receiving a snapshot between a remote merge and a
concurrent insert into the merged-away parent.

## Fix

Persist the **single** `MergedFrom` field in the snapshot encoding and
derive everything else at load time. `TreeNode` still has four runtime
fields (`MergedFrom`, `mergedInto`, `mergedChildIDs`, `mergedAt`), but
only `MergedFrom` is the source of truth — the other three are caches:

- During a local merge: `Tree.mergeNodes` sets `MergedFrom` on the
  moved child and derives the caches, same as before.
- On snapshot load: `Tree.rebuildMergeState` walks the tree (after
  `NodeMapByID` is populated) and reconstructs the caches from
  `MergedFrom`:
  - `mergedAt = source.removedAt` (merge tombstones source in same op)
  - `source.mergedInto = current-parent.id`
  - `source.mergedChildIDs = [siblings with MergedFrom == source.id]`

Proto change is backwards-compatible (new optional field #9 in
`TreeNode`). Old clients ignore the field; new clients that load an old
snapshot without `MergedFrom` behave exactly as before the fix (i.e.
still carry the latent bug for pre-fix data, but no new regression).

## Files touched

- `api/yorkie/v1/resources.proto` — new `merged_from` field on `TreeNode`
- `api/yorkie/v1/resources.pb.go` — regenerated via `make proto`
- `api/converter/to_bytes.go` — write `MergedFrom`
- `api/converter/from_pb.go` — read `MergedFrom`
- `pkg/document/crdt/tree.go` — export `mergedFrom` -> `MergedFrom`;
  add `rebuildMergeState` called from `NewTree`
- `api/converter/converter_test.go` — two regression tests
  (`tree_merge-and-insert_convergence_across_snapshot`,
  `tree_merge-and-merge_convergence_across_snapshot`)
- `docs/design/concurrent-merge-split.md` — Fix 4 / Fix 8 / Design
  Decisions / Alternatives updated

## Non-Goals

- Fixing the 3 still-skipped tests from `20260406-concurrent-merge-split-todo.md`
  — those are unrelated CreatedAt visibility issues.
- JS SDK changes — follow-up; JS clients will ignore the new field
  until mirrored there.

## Review

The original review goal ("reduce 4 fields to 2 on `TreeNode`") has
been reinterpreted: instead of deleting the three derived fields, they
are demoted to runtime caches with a single persisted source of truth.
This preserves the code paths that use them (no disruption to existing
Fix 3/4/5/8 logic) while making the minimum possible change to the
wire format. In practice this is even better than the original plan —
`MergedFrom` alone captures the merge relationship, and the derived
fields are recomputed deterministically at load time.

Key lesson: "runtime-only" is not a free design choice when the runtime
state must outlive a snapshot. The diagnostic test at `api/converter`
(no server, no monkey patch) was the right granularity to surface this
cheaply.
