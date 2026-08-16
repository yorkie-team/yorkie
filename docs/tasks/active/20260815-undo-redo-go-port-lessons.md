# Undo/Redo Go SDK Port — Lessons

**Created**: 2026-08-15

Design: `docs/design/undo-redo-go-port.md`
Plan: `20260815-undo-redo-go-port-todo.md`

## Discovered while designing

- The server-side half of undo/redo was already in Go (`Restore`,
  `Retombstone`, `restoreMode`, the restore wire format). Only the producer
  side was missing. Scoping the port off "what does Go already execute" rather
  than "what does JS have" cut the estimate substantially.
- `operation.execute` in JS takes an `OpSource`, which Go has no concept of.
  `Set` and `Remove` genuinely branch on it during undo. This was invisible
  from the type signatures and only surfaced by reading the JS bodies.
- Reverse operations accumulate with `unshift`, not `push`. Forward order
  silently breaks undo of chained operations within one change.
- Producing a reverse needs pre-mutation state that Go's CRDT mutators discard:
  JS `text.edit()` returns six values, Go's returns four. The port therefore
  touches two signature layers, not one.
- Go's `Update` holds `d.mu`, so calling `Undo()` from inside an updater
  deadlocks where JS merely throws. The guard has to be an atomic flag checked
  **before** the lock, not a field read inside it.

## Learned during implementation

- **A JS `it.skip` is not evidence of a live defect — it is a claim that has
  to be checked, not trusted.** `history_text_test.ts`'s "Case 3/5
  correctness" pair (`:705`, `:742`) was still marked `.skip` in JS, citing
  overlapping-undo content duplication, but JS's own #1293
  ("Identity-preserving restore for Text undo/redo", 2026-07-23) had already
  fixed the underlying mechanism months before this port started — nobody had
  re-run the skipped pair to remove the citation. Task 13 ported both cases
  live anyway instead of carrying the skip forward, and they passed. By
  contrast, `history_tree_split_test.ts`'s `split-l2 → split-l2` skip was
  tested the same way — Task 20 ran it with the skip bypassed as a one-shot
  experiment — and it genuinely still fails, in both SDKs, with the same
  malformed-nesting shape the `TODO(#1235)` describes. The lesson is not "old
  skips are usually stale" or "old skips are usually still valid" — it is
  that neither assumption is safe, and the cost of running the actual
  experiment (temporarily bypass the skip, run the case, revert) is low
  enough that there is no excuse for carrying a skip forward on citation
  alone.
- **Grep-derived test counts are worthless for parameterized suites.**
  `history_array_test.ts` is 2 `it(` call sites but 84 runtime instances;
  `history_tree_test.ts` is 32 by the same grep but 135. Both numbers were
  wrong in an early draft of this plan and misled three separate tasks before
  the counting method was corrected to "read the loop bounds, multiply out
  the Cartesian product, then confirm against `go test -v`'s one
  `--- PASS`/`--- SKIP` line per runtime instance" (see this document's
  "Parity audit" section below, and `undo-redo-go-port.md`'s "Counting
  method" note). Any test-count claim not derived this way should be treated
  as a guess, regardless of how confidently it is stated.
- **A marshal-only assertion passes while an identity or index bug is live.**
  The recurring defect class in this port was not visible in rendered output:
  a reverse that re-inserts a node under an id its own tombstone still holds
  makes GC purge the *live* node instead of the dead one (only detectable by
  asserting `GarbageLen()` / duplicate-ID checks, not `Marshal()`); a
  CRDT-level identity fix was twice silently undone downstream — once by
  `DeepCopy`, once by snapshot decode — because neither preserves identity by
  default and nothing asserted on it; duplicate `TreeNodeID`s make a document
  permanently unloadable in a way that only a fresh decode of a persisted
  snapshot reveals, not an in-memory check. What actually caught these: GC
  assertions with explicit garbage counts after undo, clone/snapshot
  round-trip tests that decode into a *second* document and compare, and
  identity checks (`treeNodeIDs`/`duplicatedTreeIDs`-style helpers) run at
  every undo, redo, and post-GC step — not just at the end of a scenario. A
  test suite for this kind of port needs to budget for identity assertions as
  a first-class category, not an afterthought bolted onto content checks.
- **An index captured at the wrong phase is silently wrong, and every
  existing test still passes.** Twice during this port, a reconciliation
  value was computed from tree/text state read *before* the operation that
  actually splits or narrows it, producing a plausible but incorrect
  reconciliation range — and nothing failed, because no existing test's
  fixture happened to exercise the phase ordering that would expose the
  difference. (See `crdt/tree.go`'s own comment on `PreEditFromIdx`'s capture
  point, added specifically to make the correct phase explicit after this was
  found once.) The lesson: when porting a value that is a snapshot of
  mutable state, name the exact phase it is captured at in a comment, and
  write at least one test whose fixture depends on that specific ordering —
  not just on the final state — or the bug has no test surface to be caught
  on.
- **"Go is also the server" changes which fixes are safe to defer and which
  are not.** Where a JS defect is *uniform and convergent* — every replica
  computes the same wrong answer — porting it as-is in Go keeps both SDKs
  wrong together, which is recoverable by a coordinated fix later (e.g. the
  Tree Style combined-reverse bug: every replica silently keeps a key that
  should have been removed). But fixing that same class of defect in Go
  *alone* would convert it into permanent server-versus-client divergence,
  since Go's `change.Execute` is also what the server replays when
  materializing snapshots from a JS client's change — strictly worse than
  leaving it alone, not better. The mirror case also held: where Go had
  *already* deliberately diverged from JS on the wire (split tickets, i.e.
  representing a `splitLevel >= 1` edit's boundary tokens explicitly rather
  than JS's implicit approach), completing that choice in the reverse-op
  layer was not a *new* divergence to weigh against parity — the divergence
  point had already been chosen and filed; the work was finishing a decision,
  not making one. Telling these apart requires asking, for every candidate
  fix: is the defect visible identically on every replica today, and does
  fixing it in Go alone change what the server would compute from a change
  a JS client sent? If the answer to the second question is yes, it is not a
  Go-only decision, no matter how small the code change looks.

## Parity audit

JS runtime instance counts cannot be independently re-derived in this
workspace — no `yorkie-js-sdk` checkout with `it(` loop bodies was available
locally for Task 21 (the counting method below relies on the derivations
already recorded by Tasks 8, 13, 18, and 20, cross-checked against a fresh
`go test -tags integration -v` run of every ported Go file). Counts are
**runtime instances**: one `--- PASS`/`--- SKIP` line per `t.Run` leaf in the
`-v` output, not `t.Run(` call sites (a single call site inside a loop
produces many instances).

| JS file | JS instances | Go file(s) | Go instances | Result |
|---|---|---|---|---|
| `history_array_test.ts` | 84 | `test/integration/history_array_test.go` (`TestHistoryArray`) | 84 (4 skipped) | Match. Skips: `move-*-set` combinations, ported identically — Array Set+Move known issue |
| `history_text_test.ts` | 73 | `test/integration/history_text_test.go` (64) + pre-existing `history_text_reconcile_test.go` (9) | 73 (0 skipped) | Match. 2 of the 9 reconcile instances were stale `it.skip` in JS (Case 3/5 correctness); ported live and pass — see "Learned during implementation" above |
| `history_tree_test.ts` | 135 | `test/integration/history_tree_test.go` | 135 (of 137 in-file instances; the other 2, `TestHistoryTreeConcurrentUndo`, are pre-existing and not JS-sourced) | Match |
| `history_tree_concurrent_test.ts` | 14 | `test/integration/history_tree_concurrent_test.go` (`TestHistoryTreeConcurrentOverlappingUndoAfterGC`) | 14 (2 skipped) | Match. Skips: JS segmentation-difference `KNOWN` cases, confirmed still real |
| `history_tree_split_test.ts` | 79 | `test/integration/history_tree_split_test.go` | 79 (1 skipped) | Match. This plan's original estimate of "26, 1 skipped" was a pre-port grep-derived guess, corrected during Tasks 18-20 as each split test file was actually built and run |

**Grand total: 385 JS instances, 385 Go instances (7 skipped: 4 array + 2 tree
concurrent + 1 tree split), 0 unaccounted gaps.**

No file has any test present in JS with no Go counterpart, or vice versa
beyond the two named pre-existing, non-JS-sourced Go additions
(`TestHistoryTreeConcurrentUndo`, `TestHistoryTreeGCSymmetryAndAnchorFallback`
— the latter is itself one of the 135 JS-matched instances, ported from JS's
"GC symmetry and anchor fallback" `describe` block, not extra).
