# splitLevel >= 2 Convergence Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Fix the 53 remaining concurrent divergences for `splitLevel >= 2` operations in the Yorkie Tree CRDT.

**Architecture:** Experiment-driven approach — classify failures first (Phase 1), then apply Fix 7 propagation in the recursive split loop (Phase 2), with parent-check relaxation and Fix 8 as follow-up phases. Each experiment records results and learnings.

**Tech Stack:** Go (server), TypeScript (JS SDK). Property-based concurrent tests in `test/complex/tree_concurrency_test.go` (Go, `//go:build complex`).

**Design doc:** `docs/design/split-level-2-convergence.md`

---

### Task 1: Establish Baseline — Run Skipped L2 Tests and Count

**Files:**
- Read: `test/complex/tree_concurrency_test.go`
- Read: `pkg/document/crdt/tree.go`

This task does not change any code. It establishes the exact failure count and collects divergent states for classification.

- [ ] **Step 1: Run SplitSplit tests and capture skip count**

Run from the yorkie repo root:

```bash
cd /Users/hackerwins/Development/yorkie-team/second-brain/03_projects/yorkie
go test -tags complex -run TestTreeConcurrencySplitSplit -v -count=1 ./test/complex/ 2>&1 | tail -100
```

Expected: ~46 tests show `--- SKIP` with `different result`. Record the exact count.

- [ ] **Step 2: Run SplitEdit tests and capture skip count**

```bash
go test -tags complex -run TestTreeConcurrencySplitEdit -v -count=1 ./test/complex/ 2>&1 | tail -100
```

Expected: ~7 tests show `--- SKIP`. Record the exact count.

- [ ] **Step 3: Run full suite to confirm baseline**

```bash
go test -tags complex -run 'TestTreeConcurrency' -v -count=1 ./test/complex/ 2>&1 | grep -E 'SKIP|PASS|FAIL' | sort | uniq -c
```

Record: total pass, total skip, total fail. This is the baseline for all experiments.

- [ ] **Step 4: Record baseline in the Experiment Log**

Update `docs/design/split-level-2-convergence.md`, Experiment 1 section:

```markdown
### Experiment 1: Failure Classification

- **Date:** 2026-04-24
- **Hypothesis:** Most of the 53 failures are F7-pos (position mismatch at ancestor level).
- **Method:** Run skipped tests, count and classify.
- **Result:** SplitSplit: _/_ skip, SplitEdit: _/_ skip. Total: _/_ skip.
- **Learnings:** (fill after classification)
```

- [ ] **Step 5: Commit baseline**

```bash
git add docs/design/split-level-2-convergence.md
git commit -m "Record baseline skip counts for splitLevel>=2 convergence experiments"
```

---

### Task 2: Classify Failures — Add Logging to Identify Root Causes

**Files:**
- Modify: `test/complex/tree_concurrency_test.go:232-248`

Add verbose logging to the test harness so that when convergence fails, we can see the divergent XML states and identify what went wrong.

- [ ] **Step 1: Add divergent-state logging to RunTestTreeConcurrency**

In `test/complex/tree_concurrency_test.go`, modify the `runTest` function to log both replicas' XML when they diverge:

```go
flag := syncClientsThenCheckEqual(t, []clientAndDocPair{{c1, d1}, {c2, d2}})
if flag {
    return testResult{flag, `pass`}
}
xml1 := d1.Root().GetTree("t").ToXML()
xml2 := d2.Root().GetTree("t").ToXML()
return testResult{flag, fmt.Sprintf("different result\n  d1: %s\n  d2: %s", xml1, xml2)}
```

Also add `"fmt"` to the import block if not already present.

- [ ] **Step 2: Run SplitSplit with verbose output and save to file**

```bash
go test -tags complex -run TestTreeConcurrencySplitSplit -v -count=1 ./test/complex/ 2>&1 | grep -A2 'SKIP' > /tmp/splitsplit-divergences.txt
```

- [ ] **Step 3: Run SplitEdit with verbose output and save to file**

```bash
go test -tags complex -run TestTreeConcurrencySplitEdit -v -count=1 ./test/complex/ 2>&1 | grep -A2 'SKIP' > /tmp/splitedit-divergences.txt
```

- [ ] **Step 4: Classify failures by examining divergent states**

Examine `/tmp/splitsplit-divergences.txt` and `/tmp/splitedit-divergences.txt`. For each failed test, classify by comparing the two XMLs:

- **F7-pos:** Nodes appear in wrong position (split sibling not advanced past)
- **F7-parent:** Split sibling in different parent than expected (parent check broke chain)
- **F8-ancestor:** Children partitioned to wrong side of split
- **NEW:** Pattern doesn't match Fix 7 or Fix 8 gap

Record classification counts in the design doc.

- [ ] **Step 5: Update Experiment Log with classification results**

Update `docs/design/split-level-2-convergence.md`, Experiment 1:

```markdown
- **Learnings:**
  - F7-pos: _ tests
  - F7-parent: _ tests
  - F8-ancestor: _ tests
  - NEW: _ tests
  - Common divergence pattern: (describe)
```

- [ ] **Step 6: Commit classification results**

```bash
git add docs/design/split-level-2-convergence.md test/complex/tree_concurrency_test.go
git commit -m "Add divergence logging and classify splitLevel>=2 test failures"
```

---

### Task 3: Fix 7 Propagation — Add advancePast to Split Loop (Go)

**Files:**
- Modify: `pkg/document/crdt/tree.go:1325-1359` (split loop)

This is the core fix: apply `advancePastUnknownSplitSiblings` at each iteration of the recursive split loop.

- [ ] **Step 1: Modify the split loop to apply Fix 7 at each iteration**

In `pkg/document/crdt/tree.go`, modify the `split` method (line 1325):

```go
func (t *Tree) split(
	fromParent *TreeNode,
	fromLeft *TreeNode,
	splitLevel int,
	issueTimeTicket func() *time.Ticket,
	versionVector time.VersionVector,
) error {
	if splitLevel == 0 {
		return nil
	}

	splitCount := 0
	parent := fromParent
	left := fromLeft
	for splitCount < splitLevel {
		// Fix 7 propagation: advance past unknown element split siblings
		// at the current ancestor level. Without this, the position marker
		// at ancestor levels does not account for concurrent splits that
		// created unknown siblings (with non-deterministic element IDs
		// discoverable only via InsNextID chain).
		if left != parent {
			left = t.advancePastUnknownSplitSiblings(left, versionVector)
		}

		var err error
		offset := 0
		if left != parent {
			offset, err = parent.Index.FindOffset(left.Index)
			if err != nil {
				return err
			}

			offset++
		}
		if _, err := parent.Split(t, offset, issueTimeTicket, versionVector); err != nil {
			return err
		}
		left = parent
		parent = parent.Index.Parent.Value
		splitCount++
	}

	return nil
}
```

- [ ] **Step 2: Run SplitSplit tests to measure improvement**

```bash
go test -tags complex -run TestTreeConcurrencySplitSplit -v -count=1 ./test/complex/ 2>&1 | grep -E 'SKIP|PASS|FAIL' | sort | uniq -c
```

Record: how many skips reduced from baseline.

- [ ] **Step 3: Run SplitEdit tests to measure improvement**

```bash
go test -tags complex -run TestTreeConcurrencySplitEdit -v -count=1 ./test/complex/ 2>&1 | grep -E 'SKIP|PASS|FAIL' | sort | uniq -c
```

- [ ] **Step 4: Run full suite to check for regressions**

```bash
go test -tags complex -run 'TestTreeConcurrency' -v -count=1 ./test/complex/ 2>&1 | grep -E 'SKIP|PASS|FAIL' | sort | uniq -c
```

Confirm: no previously-passing tests now fail. Total pass count must be >= 1539.

- [ ] **Step 5: Run unit tests for regressions**

```bash
make test
```

All existing tests must pass.

- [ ] **Step 6: Update Experiment Log**

Update `docs/design/split-level-2-convergence.md`, Experiment 2:

```markdown
### Experiment 2: Fix 7 Propagation

- **Date:** 2026-04-2X
- **Hypothesis:** Adding advancePastUnknownSplitSiblings in the split loop resolves F7-pos failures.
- **Change:** Added Fix 7 call at each iteration of the split loop in tree.go:split()
- **Result:** _/53 tests fixed. New totals: _ pass, _ skip.
- **Regressions:** _/1539 existing tests.
- **Learnings:** (observations about which tests were fixed, which remain)
```

- [ ] **Step 7: Commit**

```bash
git add pkg/document/crdt/tree.go docs/design/split-level-2-convergence.md
git commit -m "Propagate Fix 7 (split sibling forwarding) into recursive split loop

advancePastUnknownSplitSiblings was only applied before the split loop
entry (Edit Step 01-1). For splitLevel >= 2, the position marker at
ancestor levels was not advanced past concurrent unknown element split
siblings. Element splits produce non-deterministic IDs (new tickets),
so InsNextID chain traversal is the only discovery path.

This change applies Fix 7 at each iteration of the recursive split
loop, matching the approach already used for Fix 11 in Style/RemoveStyle."
```

---

### Task 4: Parent Check Relaxation — Handle Moved Siblings (Go)

**Files:**
- Modify: `pkg/document/crdt/tree.go:1288-1323` (advancePastUnknownSplitSiblings)

If Task 3 does not resolve all failures (particularly F7-parent cases), relax the parent equality check following the Fix 11 rationale.

**Skip this task if Task 3 resolves all 53 failures.**

- [ ] **Step 1: Add a relaxParentCheck parameter to advancePastUnknownSplitSiblings**

In `pkg/document/crdt/tree.go`, modify the function signature and logic:

```go
// advancePastUnknownSplitSiblings follows the InsNextID chain of the given
// node, advancing past element-type split siblings that the editing client
// did not know about (not in versionVector).
//
// When relaxParentCheck is true, the parent-equality guard is skipped.
// This is needed at ancestor levels of recursive splits (splitLevel >= 2)
// where a concurrent ancestor split may have moved the sibling to a
// different parent — same rationale as hasUnknownSplitSibling (Fix 11).
func (t *Tree) advancePastUnknownSplitSiblings(
	node *TreeNode,
	versionVector time.VersionVector,
	relaxParentCheck ...bool,
) *TreeNode {
	if len(versionVector) == 0 || node == nil {
		return node
	}

	relaxParent := len(relaxParentCheck) > 0 && relaxParentCheck[0]

	current := node
	for current.InsNextID != nil {
		next := t.findFloorNode(current.InsNextID)
		if next == nil || next.IsText() {
			break
		}

		// Stop if the sibling has been moved to a different parent
		// (e.g., by a higher-level concurrent split).
		// Skip this check when relaxParentCheck is true (ancestor split level).
		if !relaxParent && next.Index.Parent != current.Index.Parent {
			break
		}

		actorID := next.id.CreatedAt.ActorID()
		if l, ok := versionVector.Get(actorID); ok && l >= next.id.CreatedAt.Lamport() {
			break
		}

		current = next
	}

	return current
}
```

- [ ] **Step 2: Update the split loop to pass relaxParentCheck=true**

In the `split` method, update the Fix 7 call:

```go
		if left != parent {
			left = t.advancePastUnknownSplitSiblings(left, versionVector, true)
		}
```

The existing callers in Edit Step 01-1 (lines 995-999) remain unchanged — they pass no third argument, so `relaxParent` defaults to `false`.

- [ ] **Step 3: Run SplitSplit and SplitEdit tests**

```bash
go test -tags complex -run 'TestTreeConcurrencySplitSplit|TestTreeConcurrencySplitEdit' -v -count=1 ./test/complex/ 2>&1 | grep -E 'SKIP|PASS|FAIL' | sort | uniq -c
```

- [ ] **Step 4: Run full suite for regressions**

```bash
go test -tags complex -run 'TestTreeConcurrency' -v -count=1 ./test/complex/ 2>&1 | grep -E 'SKIP|PASS|FAIL' | sort | uniq -c
```

No previously-passing tests may fail. Also run `make test`.

- [ ] **Step 5: Update Experiment Log**

Update `docs/design/split-level-2-convergence.md`, Experiment 3:

```markdown
### Experiment 3: Parent Check Relaxation

- **Date:** 2026-04-2X
- **Hypothesis:** Relaxing parent equality check (Fix 11 style) resolves F7-parent failures.
- **Change:** Added relaxParentCheck parameter, used in split loop only.
- **Result:** _/53 tests fixed (cumulative with Experiment 2). New totals: _ pass, _ skip.
- **Regressions:** _/1539 existing tests.
- **Learnings:**
```

- [ ] **Step 6: Commit**

```bash
git add pkg/document/crdt/tree.go docs/design/split-level-2-convergence.md
git commit -m "Relax parent equality check in split loop Fix 7 propagation

In multi-level splits (splitLevel>=2), a concurrent ancestor split may
move the split sibling to a different parent. The parent equality guard
in advancePastUnknownSplitSiblings breaks the chain prematurely.

Follow Fix 11 (hasUnknownSplitSibling) rationale: InsNextID is only set
by SplitElement, so its existence is sufficient evidence of a split
sibling. Parent check is relaxed only at ancestor iterations of the
split loop; initial-level callers retain the strict check."
```

---

### Task 5: Port Fixes to JS SDK

**Files:**
- Modify: `packages/sdk/src/document/crdt/tree.ts:1661-1676` (split loop)
- Modify: `packages/sdk/src/document/crdt/tree.ts:968-1002` (advancePastUnknownSplitSiblings)

Port whichever fixes succeeded in Tasks 3-4 to the JS SDK.

- [ ] **Step 1: Add Fix 7 propagation to JS split loop**

In `packages/sdk/src/document/crdt/tree.ts`, modify the split loop (line 1661):

```typescript
    // 04. Split: split the element nodes for the given split level.
    if (splitLevel > 0) {
      let splitCount = 0;
      let parent = fromParent;
      let left = fromLeft;
      while (splitCount < splitLevel) {
        // Fix 7 propagation: advance past unknown element split siblings
        // at the current ancestor level.
        if (left !== parent) {
          left = this.advancePastUnknownSplitSiblings(left, versionVector, true);
        }

        parent.split(
          this,
          parent.findOffset(left, true) + 1,
          issueTimeTicket,
          versionVector,
        );
        left = parent;
        parent = parent.parent!;
        splitCount++;
      }
```

- [ ] **Step 2: Add relaxParentCheck parameter to JS advancePastUnknownSplitSiblings**

Modify the method (line 968):

```typescript
  private advancePastUnknownSplitSiblings(
    node: CRDTTreeNode,
    versionVector?: VersionVector,
    relaxParentCheck = false,
  ): CRDTTreeNode {
    if (!versionVector || !node) {
      return node;
    }

    let current = node;
    while (current.insNextID) {
      const next = this.findFloorNode(current.insNextID);
      if (!next || next.isText) {
        break;
      }

      // Stop if the sibling has been moved to a different parent
      // (e.g., by a higher-level concurrent split).
      // Skip this check when relaxParentCheck is true (ancestor split level).
      if (!relaxParentCheck && next.parent !== current.parent) {
        break;
      }

      const actorID = next.id.getCreatedAt().getActorID();
      const knownLamport = versionVector.get(actorID);
      if (
        knownLamport !== undefined &&
        knownLamport >= next.id.getCreatedAt().getLamport()
      ) {
        break;
      }

      current = next;
    }

    return current;
  }
```

- [ ] **Step 3: Build and lint**

```bash
cd /Users/hackerwins/Development/yorkie-team/second-brain/03_projects/yorkie-js-sdk
pnpm lint && pnpm sdk build
```

- [ ] **Step 4: Run tree tests**

```bash
pnpm sdk test test/integration/tree_test.ts
```

All existing tests must pass. No new test failures.

- [ ] **Step 5: Run history tree tests**

```bash
pnpm sdk test test/integration/history_tree_test.ts
```

Verify no regressions in undo/redo tests.

- [ ] **Step 6: Commit**

```bash
git add packages/sdk/src/document/crdt/tree.ts
git commit -m "Port Fix 7 propagation for splitLevel>=2 to JS SDK

Mirror the Go server change: apply advancePastUnknownSplitSiblings at
each iteration of the recursive split loop with relaxed parent check.
Element split siblings have non-deterministic IDs (new tickets) and
are only discoverable via InsNextID chain traversal."
```

---

### Task 6: Record Final Results and Update Design Docs

**Files:**
- Modify: `docs/design/split-level-2-convergence.md`
- Modify: `docs/design/concurrent-merge-split.md:183-191`

- [ ] **Step 1: Run final full test suite (Go)**

```bash
cd /Users/hackerwins/Development/yorkie-team/second-brain/03_projects/yorkie
go test -tags complex -run 'TestTreeConcurrency' -v -count=1 ./test/complex/ 2>&1 | grep -E 'SKIP|PASS|FAIL' | sort | uniq -c
```

Record final pass/skip/fail counts.

- [ ] **Step 2: Update convergence coverage table in concurrent-merge-split.md**

Update the "Convergence Coverage" section (line 146) and the "Remaining Issue" section (line 183) with the new test results:

```markdown
## Remaining Issue: `splitLevel >= 2`

[Update with current status — how many of the 53 divergences are resolved,
how many remain, and what the remaining root causes are.]
```

- [ ] **Step 3: Write final Experiment Log entries**

Complete all experiment entries in `docs/design/split-level-2-convergence.md` with actual results and learnings.

- [ ] **Step 4: Commit**

```bash
git add docs/design/split-level-2-convergence.md docs/design/concurrent-merge-split.md
git commit -m "Update convergence docs with splitLevel>=2 experiment results"
```

---

### Task 7: Write Lessons Learned

**Files:**
- Create: `docs/tasks/active/20260424-split-level-2-convergence-lessons.md`

- [ ] **Step 1: Write lessons file**

Create `docs/tasks/active/20260424-split-level-2-convergence-lessons.md` with learnings from all experiments, including:

- Which failures were Fix 7 vs Fix 8 vs NEW
- Whether parent check relaxation was needed
- Text vs Element ID asymmetry implications
- Any unexpected findings
- What remains for undo/redo enablement

- [ ] **Step 2: Update active tasks README**

Update `docs/tasks/active/README.md`:

```markdown
---
updated: 2026-04-24
---

# Active Tasks

| Task | Status |
|------|--------|
| [splitLevel>=2 Convergence](20260424-split-level-2-convergence-todo.md) | in progress |
```

- [ ] **Step 3: Commit**

```bash
git add docs/tasks/active/
git commit -m "Add splitLevel>=2 convergence task docs and lessons"
```
