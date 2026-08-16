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
  are not — but the corollary needs a real example, not an assumed one.**
  Where a JS defect is *uniform and convergent* — every replica computes the
  same wrong answer — porting it as-is in Go keeps both SDKs wrong together,
  which is recoverable by a coordinated fix later (e.g. the Tree Style
  combined-reverse bug: every replica silently keeps a key that should have
  been removed). Fixing that class of defect in Go *alone*, without a version
  gate, would convert it into permanent server-versus-client divergence,
  since Go's `change.Execute` is also what the server replays when
  materializing snapshots from a JS client's change. (The filed ruling for
  that bug leaves a version-gated Go-side fix open as one option, so this is
  a caution about *unconditional* one-sided fixes, not a claim that Go must
  always wait for JS.) An earlier draft of this lesson claimed a mirror
  case — that Go had already deliberately diverged from JS by putting split
  tickets on the wire, so completing that choice was not a new divergence —
  and that claim did not survive review: both SDKs carry the field
  identically (`api/yorkie/v1/resources.proto:138-142`,
  `packages/sdk/src/api/converter.ts:615`/`:1554`, and JS's own
  `split_ticket_test.ts`), so there was no such divergence to complete. This
  port did not turn up a clean real example of that shape. The two questions
  worth asking for every candidate fix still stand on their own, independent
  of that missing example: is the defect visible identically on every
  replica today, and does fixing it in Go alone change what the server would
  compute from a change a JS client sent? If the answer to the second
  question is yes, it is not a Go-only decision, no matter how small the
  code change looks — but verify any claimed precedent for an exception
  against the actual source before writing it down, the way this one was not.
  (Critical 3 of this task's own review — a validation Go was missing that
  JS already enforces, `fromTreeRestoreSpans`'s attribute `updatedAt` check —
  is the shape a genuinely safe Go-only fix takes: JS already validates, so
  fixing Go's gap closes an asymmetry rather than opening one.)

## Parity audit

A `yorkie-js-sdk` checkout pinned at this port's exact target commit
(`28a5a42e`, v0.7.16) is available in this workspace at `../yorkie-js-sdk`.
An earlier pass of this audit wrongly stated no JS checkout was available
and derived JS counts from prior task reports instead of the source; this
table was re-derived directly from `../yorkie-js-sdk`'s five history test
files, then cross-checked against a fresh `go test -tags integration -v`
run of every ported Go file. Counts are **runtime instances**: one
`--- PASS`/`--- SKIP` line per `t.Run` leaf in the `-v` output, not `t.Run(`
call sites (a single call site inside a loop produces many instances).

**Counting-method blind spot, found in review.** A Go test ported as a bare
`func Test...(t *testing.T)` with zero `t.Run` calls inside it (one JS `it`
that needed no parameterization) emits exactly one top-level
`--- PASS: TestFoo` line with no indentation and no `/` — invisible to a
naive "count indented leaf lines" pass, which would silently undercount by
one per such function. `test/integration/history_tree_test.go`'s
`TestHistoryTreeUndoPastInitialRoot` is exactly this shape. This has to be
added back in by hand, function by function, not by adjusting a grep
pattern — see the table below, where it is accounted for explicitly.

| JS file | JS instances | Go file(s) | Go instances | Result |
|---|---|---|---|---|
| `history_array_test.ts` | 84 | `test/integration/history_array_test.go` (`TestHistoryArray`) | 84 (4 skipped) | Match. Skips: `move-*-set` combinations, ported identically — Array Set+Move known issue |
| `history_text_test.ts` | 73 | `test/integration/history_text_test.go` (64) + pre-existing `history_text_reconcile_test.go` (9) | 73 (0 skipped) | Match. 2 of the 9 reconcile instances were stale `it.skip` in JS (Case 3/5 correctness); ported live and pass — see "Learned during implementation" above |
| `history_tree_test.ts` | 135 | `test/integration/history_tree_test.go` | 135 (of 137 in-file instances; the other 2, `TestHistoryTreeConcurrentUndo`, are pre-existing and not JS-sourced) | Match — see derivation below |
| `history_tree_concurrent_test.ts` | 14 | `test/integration/history_tree_concurrent_test.go` (`TestHistoryTreeConcurrentOverlappingUndoAfterGC`) | 14 (2 skipped) | Match. Skips: JS segmentation-difference `KNOWN` cases, confirmed still real |
| `history_tree_split_test.ts` | 79 | `test/integration/history_tree_split_test.go` | 79 (1 skipped) | Match. This plan's original estimate of "26, 1 skipped" was a pre-port grep-derived guess, corrected during Tasks 18-20 as each split test file was actually built and run |

**`history_tree_test.ts`/`.go`, re-derived and shown in full because review
flagged this row as wrong (136, not 135).** Reading `../yorkie-js-sdk`'s
`history_tree_test.ts` directly: 32 `it(` call sites total (confirmed by
`grep -noE '^\s*it(\.only|\.skip)?\('`), multiplying out each loop —

| block | derivation | instances |
|---|---|---|
| single client basic | 6 ops + 2 fixed | 8 |
| single client chained ops | 3×3×3 | 27 |
| single client edge cases | 6 fixed | 6 |
| multi client basic | 3×3 × {undo, redo} | 18 |
| reconcile cases | Case 1–7 | 7 |
| multi client edge cases | 3 fixed | 3 |
| tree style undo/redo | 4 (5a) + 15 (5b, 4×4 − 1 excluded) + 2 (5c) | 21 |
| multi client style undo convergence | 3×3×2 | 18 |
| multi client style vs edit/split convergence | 3×4 combos × 2 directions | 24 |
| undo past initial tree via initialRoot | 1 fixed | 1 |
| GC symmetry and anchor fallback | 2 fixed | 2 |
| **Total** | | **135** |

— giving 135, matching a fresh function-by-function `go test -tags
integration -v -run '^(...)$'` run of every JS-sourced function in
`history_tree_test.go` (`TestHistoryTreeUndoPastInitialRoot` counted as 1
despite having zero `t.Run` leaves, per the blind spot above):
`TestHistoryTreeReconcileCases`=7, `SingleClientBasic`=8,
`SingleClientChainedOps`=27, `SingleClientEdgeCases`=6,
`MultiClientBasic`=18, `MultiClientEdgeCases`=3, `StyleUndoRedo`=21,
`MultiClientStyleUndoConvergence`=18, `MultiClientStyleVsEditConvergence`=24,
`UndoPastInitialRoot`=1, `GCSymmetryAndAnchorFallback`=2 — sum 135.
`TestHistoryTreeConcurrentUndo`'s 2 subtests are additional and excluded
from both totals (pre-existing, not JS-sourced), consistent with Task 18's
own accounting.

This does not confirm the review's specific "136" figure, and the grand
total below stays at 385, not 386 — logged as a disagreement rather than
silently overridden, since the underlying claim ("a bare-func test is
invisible to naive leaf-counting") is correct and worth the caveat above,
even though the total this particular row lands on, re-derived three
independent ways against the pinned JS source, is 135.

**Grand total: 385 JS instances, 385 Go instances (7 skipped: 4 array + 2 tree
concurrent + 1 tree split), 0 unaccounted gaps.**

No file has any test present in JS with no Go counterpart, or vice versa
beyond the two named pre-existing, non-JS-sourced Go additions
(`TestHistoryTreeConcurrentUndo`, `TestHistoryTreeGCSymmetryAndAnchorFallback`
— the latter is itself one of the 135 JS-matched instances, ported from JS's
"GC symmetry and anchor fallback" `describe` block, not extra).

## Phase 0 behavior-neutrality: did it hold?

**Yes.** Phase 0 (`OpSource`, `Operation.Execute`/`Change.Execute` signature
widening, `change.Context` reverse collection, `History`,
`Document.Undo/Redo/CanUndo/CanRedo/ClearHistory`) is its own commit,
stacked first and never squashed into the phases that build on it, and its
own stated done-condition — every reverse is nil, no behavior change,
existing tests green — was the acceptance bar for that commit before Phase 1
started. No task report or review round in this port found a Phase-0-caused
regression in Object, Array, Counter, Text, or Tree behavior predating the
reverse-operation work; every defect this port's reviews found (Critical and
Important alike) traces to a later phase's own new logic — reverse-operation
construction, reconciliation, or a converter/validation gap — not to the
Phase 0 signature-plumbing commit itself. This is why the PR body asks
reviewers not to squash: Phase 0's neutrality is only auditable as long as it
stays a diff of its own, isolated from the phases whose new behavior it
makes possible.

## Deferred findings roll-up

Collected from every task report's "Concerns"/"Fix report" sections plus
the two collecting task docs this port produced
(`docs/tasks/active/20260816-remote-redo-replica-divergence-todo.md`,
`docs/tasks/active/20260816-tree-style-combined-reverse-dropped-todo.md`).
Kept here, not only in the (git-ignored) `.superpowers/sdd/` task report, so
it survives past this session. Grouped by area; none of these were fixed as
part of Task 21 except where marked.

### Cross-SDK divergences (filed as their own task docs)

- Remote redo of a restored key can delete it on a peer (`Set`'s reverse
  reuses the original `createdAt` instead of re-ticketing it; the
  `OpSourceUndoRedo`-gated deregister step at `set.go:102-104` never fires
  on `OpSourceRemote`) — replica divergence, not cosmetic. Identical in JS.
- `Presence.Initialize` leaves `Document.clonePresences` stale — a later
  `Update`'s `Set` on an unrelated key drops the attach-time presence key.
  Go twin of JS's tracked `#608`.
- Undoing a newly-introduced presence key: Go keeps the zero value (`""`);
  JS drops the key entirely. Deliberately left divergent, pinned by a
  characterization test.
- `Text.Style`/`RemoveStyle` don't skip tombstoned nodes, unlike JS's
  `setStyle`/`removeStyle`.
- `validateRestoreIdentities` can reject a client's own undo under a
  GC-disabled attach (`ID.SyncLamport`) — Go-only hardening JS has no
  equivalent of, reachable for both Text and Tree undo since Task 15.
- The no-op fallback `Edit` reverse breaks a remote replica's
  `ApplyChanges` entirely when `N > 0`, because `isUndoOp` is local-only
  state never serialized. Present identically in JS.
- A snapshot round trip resurrects a text node's tombstoned attribute
  (missing `is_removed` on the wire for `Text`'s attribute-encode path).
  Present identically in JS; pre-dates this port by years.
- A Tree reverse can delete live neighbours when its content was born
  tombstoned under a concurrently-removed parent — JS shares the width
  defect; Go additionally differs in the anchor.
- **Tree Style combined reverse only restores, never removes, on execute**
  (its own task doc) — a combined restore+removal reverse silently drops
  the removal half, because `TreeStyleOperation.execute` is an if/else
  where Text's equivalent is two independent `if`s. Convergent (every
  replica drops the removal identically), reachable in ordinary
  single-client editing. Must not be fixed in Go alone: Go is also the
  server, so a one-sided fix would turn a uniform bug into permanent
  server-vs-client divergence.
- Rejecting a negative `splitLevel` (Task 19) applies new strictness
  retroactively to stored changes via the shared decode path — open
  question: does any already-persisted change carry one? Needs a
  production data audit before merge, not a code decision.

### Text / CRDT layer

- `[]PrevAttrs` slice ordering is non-deterministic (Go map iteration) when
  multiple keys are styled in one call. Flagged for any future code that
  might assume stable ordering.
- `refinePos`'s `contentLen()`/`Len()` asymmetry (first node measured
  including removed characters, later nodes excluding them) reads as a bug
  in isolation; it's a ported JS quirk.
- The restore-anchor fix changes server-side replay behavior for any
  client (including JS ones) that falls through to the from-position rung
  — a behavior change on a shared path, though it moves Go toward parity.
- **Fixed in Task 21**, not deferred: `fromTreeRestoreSpans` accepted a
  nil attribute `updatedAt`, a Go-only gap versus JS's own validation — see
  `undo-redo-go-port.md`'s "Critical 3" note.

### Tree layer

- Go's `Tree.Edit` copy-reinsert path accumulates garbage monotonically
  across undo/redo cycles — inherent to the copy path; JS mostly avoids it
  by taking the identity-preserving path instead.
- The reverse-building deep copy runs on the remote/server path too
  (`Execute` doesn't gate on `OpSource`), so the server pays a `DeepCopy`
  per tree deletion for reverse-op construction it will never use.
- The "live node in `removed` whose parent is in `preTombstoned`" union
  rule is only exercised by a direct unit test — the real trigger needs
  concurrency no single-client scenario in this port's suite reaches.
- `TestHistoryTreeReconcileCases` doesn't discriminate the new
  reconciliation arithmetic from its absence — every scenario it builds
  reaches the identity-preserving path instead.
- `splitReverseAt` propagates `FindPos`'s error rather than skipping
  (deliberate JS parity), but this is the one place in the whole change
  where a forward merge edit could newly fail `Execute` where it
  previously silently no-op'd.
- Garbage accumulates across split undo/redo cycles by design (each
  re-split mints fresh nodes; each boundary deletion tombstones the
  previous ones) — matches JS.
- `docs/design/tree-split-undo-redo.md` undercounts its own test-matrix
  sections D and H relative to the JS source's current shape.
- Three properties remain entirely unproven by any test in either SDK:
  split level 3+, a split reverse followed by another op in the same undo
  entry, and a direct assertion on `Tree.split`'s iteration-bound coupling.
- Tree's Phase 3 range-narrowing is missing a suppression guard JS has
  (`if (toLeft !== toParent)`) — Go narrows in a case JS deliberately does
  not, producing a backwards range that suppresses an intended merge, by
  JS's own reasoning. Not reached by any test in this port's suite. (Found
  in Task 21; corrected from an earlier, backwards description of this
  same divergence — see `undo-redo-go-port.md`'s "Three pre-existing
  divergences" section for the full correction.)
- The snapshot-branch local-changes replay uses `OpSourceRemote` where JS
  uses `OpSource.Local` — Go-only, no JS bug to file against, but a source
  mismatch on a path other parts of this port depend on `OpSource` being
  correct for. (Found in Task 21.)
- Go emits no `Snapshot` `DocEvent` after `applySnapshot`, unlike JS's
  `DocEventType.Snapshot`. (Found in Task 21.)

### Presence

- `Presence.Clear()` leaves `reversePresenceKeys` populated (Task 7).

### Array / Object / core history

- `isRemovedOrOrphaned`'s O(document size) tree-walk-per-call is acceptable
  for the infrequent Object Set/Remove undo path it was built for, but
  flagged as a cost worth revisiting if a hotter-path caller appears.
- `ElementRHT.Set`'s delegation to `SetWithExecutedAt` picks up a
  `!node.isRemoved()` LWW guard fix "for free" for all existing callers,
  not just the new undo/redo path — a behavior change to a pre-existing
  method outside that task's stated file list. Every test passes.
- `Counter.Increase` has unchecked negation overflow at
  `math.MinInt32`/`math.MinInt64` — narrow, pre-dates this change.
- `EditT`/`StyleByIndex` deliberately kept their old 3-tuple signatures
  rather than widening to match `Edit`/`Style` (no production caller for
  either).

### Still-open, pre-existing (tracked in `undo-redo.md`, unchanged by this port)

- Array Set + Move undo restores at the dead position, not the moved one
  (needs a proto change) — 4 tests skipped identically in both SDKs.
- GC vs. undo interaction (#664) — GC can purge elements still referenced
  by an undo/redo stack.
- History reconciliation is an O(n) stack scan; an indexed lookup would be
  faster. Same in both SDKs.
- Tree overlapping-undo content duplication for the copy-reinsert fallback
  path (Text's identity-preserving path already fixed its half — see the
  stale-skip finding above; Tree's fallback path remains open).
