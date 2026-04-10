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

### Phase 1 — Diagnostic + minimal snapshot fix (commit `93f0d14e`)

- [x] Add unit-level test at the `api/converter` layer that roundtrips
      the document state through `SnapshotToBytes` between a merge and a
      concurrent insert.
- [x] Run the test; confirm runtime-only assumption is WRONG.
- [x] Add `merged_from` optional field to `TreeNode` in `resources.proto`
      and run `make proto`.
- [x] Export `TreeNode.mergedFrom` to `MergedFrom` in `crdt/tree.go`.
- [x] Write `MergedFrom` in `to_bytes.go`; read it in `from_pb.go`.
- [x] Add `Tree.rebuildMergeState` post-load pass in `crdt/tree.go`.
- [x] Un-skip `TestConverter/tree_merge-and-insert_convergence_across_snapshot`.
- [x] Add sibling `tree_merge-and-merge_convergence_across_snapshot` test.
- [x] Update `docs/design/concurrent-merge-split.md`.
- [x] Run `go test ./...` — all green.

### Phase 2 — Simplification attempt (commit `5fbeb485`)

- [x] Remove `mergedChildIDs` field; recompute on demand via
      `target.Children(true)` filtered by `MergedFrom`.
- [x] Remove `mergedAt` field; read `source.removedAt` at use sites
      (Fix 8 in `SplitElement`, post-load `rebuildMergeState`).
- [x] Update `propagateMergeDeletes` and the redirect branch in
      `FindTreeNodesWithSplitText` to use the derived list.
- [x] Run full suite — passed.

### Phase 3 — CodeRabbit review fix (commit `9da80a0e`)

- [x] Verified CodeRabbit's concern: `TreeNode.remove()` overwrites
      `removedAt` via LWW when a later concurrent tombstone targets the
      same node, so `source.removedAt` cannot substitute for the
      merge-time causal boundary required by Fix 8.
- [x] Restore `MergedAt` as an immutable field on moved children,
      written once by `mergeNodes` at merge time.
- [x] Add `merged_at` optional field to `TreeNode` proto; regenerate.
- [x] Write `MergedAt` in `to_bytes.go`; read it in `from_pb.go`.
- [x] Keep `rebuildMergeState` fallback to `source.removedAt` for
      backwards compatibility with pre-Phase-3 snapshots.
- [x] Add `TestTreeMerge/MergedAt_is_captured_at_merge_time` unit
      regression asserting the immutability invariant.
- [x] Fix markdownlint MD040 in this task doc.
- [x] Update design doc (Fix 8 description + Design Decisions +
      Alternatives) to reflect the persisted `MergedAt`.
- [x] Run full suite — all green.

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

## Final design

`TreeNode` has **3** merge-related fields after this PR (down from the
original 4 — `mergedChildIDs` was eliminated as safely derivable):

| Field | Storage | Set when | Read where |
|---|---|---|---|
| `MergedFrom *TreeNodeID` | **persisted** (proto `merged_from`) | merge-time, on the moved child, immutable | rebuild, Fix 8 check, redirect scans |
| `MergedAt *time.Ticket` | **persisted** (proto `merged_at`) | merge-time, on the moved child, immutable | Fix 8 version-vector check |
| `mergedInto *TreeNodeID` | runtime cache | set on source parent locally (`mergeNodes`) or rebuilt on snapshot load (`rebuildMergeState`) | `FindTreeNodesWithSplitText` hot-path nil check + redirect |

Derived on demand (no field):

- **List of moved children for a given source** —
  `target.Children(true) | where MergedFrom == source.id`.
  Used by `propagateMergeDeletes` and the redirect branch in
  `FindTreeNodesWithSplitText`.

### Why `MergedAt` is persisted, not derived

The Phase 2 simplification tried to derive the merge ticket from
`source.removedAt`, reasoning that the merge tombstones the source in
the same operation so the tickets coincide at merge time. CodeRabbit
caught the bug: `TreeNode.remove()` overwrites `removedAt` under LWW
when a later concurrent delete targets the same (already tombstoned)
node. In that window the "derived" ticket becomes the delete ticket
instead of the merge ticket, and `SplitElement`'s version-vector check
(Fix 8) flips for editors whose VV covers the merge but not the later
delete. Persisting `MergedAt` as an immutable, per-child witness
eliminates the coupling to the mutable source.

### Backwards compatibility

Both new proto fields are optional additions (field numbers 9 and 10).
Old clients ignore them. New clients loading a pre-Phase-3 snapshot
fall back to `source.removedAt` in `rebuildMergeState`, which matches
the Phase 1 behavior (correct except for the rare
merge-then-later-delete corner case — no new regression for old data).

## Files touched

- `api/yorkie/v1/resources.proto` — new `merged_from` (#9) and
  `merged_at` (#10) fields on `TreeNode`
- `api/yorkie/v1/resources.pb.go` + `api/docs/yorkie/v1/*.openapi.yaml`
  — regenerated via `make proto`
- `api/converter/to_bytes.go` — write `MergedFrom` and `MergedAt`
- `api/converter/from_pb.go` — read `MergedFrom` and `MergedAt`
- `pkg/document/crdt/tree.go`:
  - Export `mergedFrom` → `MergedFrom` and `mergedAt` → `MergedAt`
  - Remove `mergedChildIDs` field; recompute on demand at two use sites
  - Add `rebuildMergeState` called from `NewTree`, reconstructs
    `mergedInto` and the `MergedAt` fallback
- `pkg/document/crdt/tree_test.go` — unit regression for the
  `MergedAt` immutability invariant
- `api/converter/converter_test.go` — two end-to-end regression tests
  (`tree_merge-and-insert_convergence_across_snapshot`,
  `tree_merge-and-merge_convergence_across_snapshot`)
- `docs/design/concurrent-merge-split.md` — Fix 4 / Fix 5 / Fix 8 /
  Design Decisions / Alternatives updated

## Non-Goals

- Fixing the 3 still-skipped tests from `20260406-concurrent-merge-split-todo.md`
  — those are unrelated CreatedAt visibility issues.
- JS SDK changes — follow-up; JS clients will ignore the new field
  until mirrored there.

## Review

The original review goal was "reduce 4 fields to 2 on `TreeNode`".
The final landing is **4 → 3**:

- `mergedChildIDs` — removed. Derivable from `target.Children(true)`
  filtered by `MergedFrom`. Both use sites already had the target in
  hand, so the cost is a short filter, not a new lookup.
- `mergedAt` — kept but repositioned. Moved from the source parent to
  the moved child, made immutable, and persisted. Cannot be derived
  from `source.removedAt` because that field is mutable under LWW.
- `mergedInto` — kept as a runtime cache. Needed for the fast nil
  check in `FindTreeNodesWithSplitText` on the position-resolution
  hot path.
- `MergedFrom` — kept, exported, persisted.

4 → 2 was the ambition; 4 → 3 is the correct answer. The gap was the
CodeRabbit review catching that the Phase 2 simplification implicitly
assumed `source.removedAt` was immutable when it is not. Correctness
beats cleverness.

Three distinct lessons came out of the work — see the sibling
`20260410-merged-fields-snapshot-lessons.md` for the full writeup.
