# Split Boundary Anchors Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans
> to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax.

**Goal:** Concurrent text inserts and paragraph splits of a text paragraph
converge (yorkie-js-sdk#1436). The split boundary records the item
it is anchored in front of, and snapshots persist that record.

**Architecture:** The innermost level of a split of an all-text node records
`BoundaryAnchor` (the first item after its position that its creator knew,
or the end of the family) and `BoundaryNest` (the same-anchor boundary it
was made inside). Splits are placed and inserts are resolved against those
records instead of §7.8 ticket order. Splits with element children or at
level ≥ 2 keep the existing §7.x rules, extended by the insert-side run of
yorkie#2171 and the walk stop of yorkie#2098. Both fields go into the
snapshot `TreeNode`. Operations do not change.

**Tech Stack:** Go, protobuf (buf), `pkg/document/crdt`, `api/converter`.

**Spec:** second-brain `03_projects/docs/design/split-boundary-representation.md`
(direction 1 and the persistence experiment). Prototype:
`split-boundary-representation/patches/direction1-anchors-go.patch`.

## Global Constraints

- Every interleaving the prototype converged must still converge. The
  broadened fuzz numbers in the brief are the bar: 0 divergences for
  insert+split, delete and style, with 2 and 3 replicas, GC and snapshot
  reload.
- Anchors are permanent node state. Never drop them on stability (the
  brief's experiment: 18–81 divergences per 5000 seeds).
- Operation contents must not carry anchors. A peer controls those bytes,
  as with `InsPrevID`/`InsNextID` (`DropSplitLinks`).
- Go is the SSOT. The JS port (yorkie-js-sdk) mirrors it file for file and
  must give identical per-seed results.
- English in files, commits and PRs. Commit subjects ≤70 characters, body
  lines ≤80.

## Review Focus

1. An anchor names an element boundary that a merge tombstones and GC
   purges. Replicas purge at different times, so the anchor must resolve
   the same way before and after the purge.
2. A snapshot from a server without the fields (old document) loads with
   no anchors. Those products must fall back to the old rules on every
   replica alike.
3. Operation contents with anchor fields set by a malicious peer must be
   ignored.
4. Large documents: anchor lookup and GC relink must not traverse the whole
   tree per call.
5. Undo/redo of a split re-creates products. Their anchors must match what
   a peer computed for the same operation.

---

### Task 1: Port the anchor rules without the experiment switches

**Files:**
- Create: `pkg/document/crdt/tree_boundary.go` (from the prototype's
  `tree_flat.go`)
- Modify: `pkg/document/crdt/tree.go`
- Test: `pkg/document/tree_boundary_insert_side_test.go`,
  `pkg/document/tree_split_order_test.go`,
  `pkg/document/gc_tree_same_boundary_split_test.go`,
  `pkg/document/crdt/tree_holds_known_child_test.go` (from the prototype)

- [x] Apply the prototype patch on `origin/main`.
- [x] Remove `flatRules` and `flatNoBarrier`, and the `else` branch of
      Phase 1-0 in `Edit`. Delete `advanceIntoSplitProducts`, which only
      that branch called.
- [x] Replace "Experiment" commentary with the rule it implements. Point
      references to `split-boundary-insert-side.md` at
      `split-boundary-anchors.md`.
- [x] `go test ./pkg/document/... ./api/converter/...` passes.
- [x] Commit, then run the code review and fix what it finds.

### Task 2: Typed anchors with indexed lookup

**Files:**
- Modify: `pkg/document/crdt/tree_boundary.go`, `pkg/document/crdt/tree.go`
- Test: `pkg/document/crdt/tree_boundary_test.go`

**Interfaces:**
- Produces: `TreeNode.BoundaryAnchor *TreeNodeID`,
  `TreeNode.BoundaryAtEnd bool`, `TreeNode.BoundaryNest *TreeNodeID`.

- [x] Write a failing test: a product anchored before a text node whose
      tombstone is purged gets relinked to the next item. Also check that
      the lookup goes through the ID map rather than a traversal.
- [x] Replace string keys with `*TreeNodeID` plus the end flag. Look nodes
      up by exact ID through `NodeMapByID`. Keep a reverse index from
      anchor ID to products so that `relinkAnchors` touches only those.
- [x] Re-run the Task 1 suites and the fuzz harnesses from the brief
      (`tree_1436_fuzz_test.go`, `tree_1436_fuzz2_test.go`). They must give
      the same per-seed result as before the change.
- [x] Commit, then run the code review and fix what it finds.

### Task 3: Persist anchors in snapshots

**Files:**
- Modify: `api/yorkie/v1/resources.proto` (`TreeNode` fields 11–13),
  generated code, `api/converter/to_bytes.go`, `api/converter/from_pb.go`,
  `api/converter/to_pb.go`
- Test: `api/converter/converter_tree_restore_test.go`,
  `pkg/document/tree_boundary_snapshot_test.go`

- [x] Write a failing test: replay the minimal snapshot-reload divergence
      from the brief. Reloading one replica mid-run must leave both
      converged.
- [x] Add `boundary_anchor`, `boundary_at_end` and `boundary_nest` to
      (later moved off `TreeNode` into `JSONElement.Tree.boundaries` as
      `TreeBoundary`, so operation content cannot carry them; see Task 7)
      `TreeNode`, and run `make proto`.
- [x] Write them in snapshots and read them back. Strip them when tree
      nodes are decoded as operation contents.
- [x] Test that a snapshot without the fields loads with no anchors.
- [x] Commit, then run the code review and fix what it finds.

### Task 4: Regression tests from the fuzz

**Files:**
- Create: `pkg/document/tree_boundary_fuzz_test.go`

- [x] Port the oracle-free convergence fuzz as a deterministic test:
      mulberry32 seeds 1–300, 2 and 3 replicas, insert/split/delete, with
      GC and snapshot reload. Keep it under 10 s in `go test`.
- [x] Add the issue's three minima and the seeds the brief names (101,
      235, 193, 24, 69, 502, 3768) as table cases.
- [x] Commit, then run the code review and fix what it finds.

### Task 5: Design doc and integration suites

**Files:**
- Create: `docs/design/split-boundary-anchors.md`
- Modify: `docs/design/README.md`, `docs/design/concurrent-merge-split.md`
  (§7.3, §7.8 point to the new doc), `docs/tasks/README.md`

- [x] Write the design from the brief: problem, the anchor rule, fallback
      scope, persistence, mixed-version risk, measurements.
- [ ] Run `test/integration` tree suites and `test/complex` tree suites.
      Not run locally: localhost:27017 is an ssh tunnel to an unknown
      remote MongoDB. CI runs them.
- [x] Run `GOTOOLCHAIN=go1.26.0 make lint`.
- [x] Commit, then run the code review and fix what it finds.

### Task 7: Undo and redo (added after the final review)

The final review found undo of a split, which merges its product away,
racing a peer's split: 82 of 1200 undo-fuzz runs that converge on main
diverged.

- [x] Seed-based undo fuzz on main and the branch, literal-script ddmin of
      the regressions, one cause per commit, each pinned in
      `TestTreeBoundaryUndoSplit` (RED before, GREEN after).
- [x] `liveSplitTarget`, `seenParentOf`, nesting skips removed boundaries,
      `boundaryMergeTarget` (changes what a concurrent merge produces), the
      tail walk passes over removed boundaries and the seen parent.
- [x] Held-out seeds 601-1800: regression rate matches the tuned seeds.
- [x] Records moved to `JSONElement.Tree.boundaries`; per-seed results
      unchanged.
- [x] Run-to-run nondeterminism found while re-measuring (GC purged a
      product before its child): `relinkSubtreeAnchors`. Two identical runs
      now give identical results.
- [x] Review of the range: merge forwarding repointed when a split carries
      merged children (`followMovedMergeChildren`, reload-consistency test);
      the seen parent read from the operation's declared parent. A
      moved-from-boundary-as-R attempt was mixed and dropped.
- [x] Regressions analysed: all real and same-script; main's convergence on
      them breaks with one snapshot reload in 2-68% of placements. Type A
      (undo/redo of an Enter at a paragraph start with the peer typing
      there) fixed by `preMergeHolder`.
- [x] Review: preMergeHolder used ticket order as causality. Four
      directions explored in separate worktrees and re-measured
      independently (two reverts, a persisted causal record, a stateless
      record fix); the stateless one adopted. Its double-Backspace record
      regression found in follow-up and fixed by keeping the smaller ticket
      of repeated merges. Declared-parent rule pinned; two cases needing
      causal knowledge recorded as skipped tests.
- [x] Repeated merges keep every merge ticket (`mergeTickets`, persisted as
      `TreeMerge`), chosen over raising `removedAt` (lost XML content) and
      a purge-time relink (depended on GC map order). The second known case
      passes. Its review found a moved-tombstone record split
      (`firstMovedChild` skip) and an insert forwarding fault (`typedAfter`);
      both pinned with tests that fail with the fix turned off.

### Task 6: PR

- [ ] Push and open the PR against `main`, linking yorkie-js-sdk#1436 and
      the brief. Mark it as a protocol change.
- [ ] Then the JS mirror (separate plan in yorkie-js-sdk).

## Review

Go side done through commit review; not pushed.

- #1436 cases (insert, split, delete, style) converge with GC and snapshot
  reloads: 0 of 9000 broadened-fuzz runs and 0 of 4000 GC runs diverge
  (main: 261-1046 per case).
- Undo and redo: 222 of 1200 and 460 of 2400 runs do not converge (main
  463 and 896), with 5 and 4 regressions against main, all reproducing on
  the same script. With snapshot reloads (reloaded replicas only pull),
  509 of 3600 (main 1066), 5 regressions. Boundary records differ in 11
  and 25 runs that converge otherwise; 29 of 3000 in the Backspace-heavy
  fuzz (66 before keeping every merge ticket). The twelve-order
  undo/split/insert set converges in all twelve (main none).
- Merge fuzz: fewer divergences than main, 4/0/5/4 regressions.
- Protocol: `JSONElement.Tree.boundaries` and `merges` (`TreeBoundary`,
  `TreeMerge`); operations and `TreeNode` unchanged. Op semantics change,
  so the server and SDKs must ship together; no version gate.
- Open: JS mirror, integration suites in CI, PR.
