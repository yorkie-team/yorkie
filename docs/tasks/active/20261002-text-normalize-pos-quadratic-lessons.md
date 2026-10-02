# Lessons: Text local edits went quadratic after the undo/redo port

**Created**: 2026-10-02

## A 1:1 port can carry a cost model that does not port

JS `normalizePos` walks the prev chain too, but `getLength()` there is a
cached field. In Go the same walk called `TextValue.Len()`, which encoded the
whole value to UTF-16 per node. The algorithm was ported faithfully; its cost
was not. When porting a loop, check what each call inside it costs on the
target side, not only what it returns.

## A stopped timer hides a regression from ns/op, not from the job

`single_text_delete_all_100000` builds its text with the timer stopped, so its
ns/op barely moved while the job spent ~6 minutes in setup. The CI bench job's
wall-clock and allocs/op caught what ns/op did not. Look at the job duration
and allocs when a bench run gets slow, not only at the comparison table.

## The bench history branch is a free bisect

`yorkie-ci-benchmark` stores one result per main push with its commit hash.
Reading `BenchmarkTextEditing` across that history found the regressing commit
(`36235fd4`) without running anything locally.

## Keep the old definition as the test oracle

The replacement reads the same sum from a different structure, so the test
compares it to the definition (sum of live lengths before the node) at every
offset of every node, tombstones included, across random edit/style/undo/redo/
GC sequences. Two deliberate mutations (dropping the in-node offset, and
counting tombstones at full width) both fail it.

## Self review log

- Round 1 (correctness/tests): no blocking findings; stopped there. Two
  non-blocking fixes applied (test message cost, stale comments). A
  `require` with O(n) message arguments is evaluated on every passing check;
  compare first and assert only on mismatch when the message is expensive.
