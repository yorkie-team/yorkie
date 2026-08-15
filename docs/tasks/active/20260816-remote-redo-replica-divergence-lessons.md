# Lessons: remote redo replica divergence

**Created**: 2026-08-16

## A local repro that "looks like a display bug" can be replica divergence

The first symptom found (Task 7's own testing) was simply: `d2.Marshal()`
stays `{}` after a peer pulls a redo that restores a key. That reads like
a sync-lag or display bug — something that would self-correct. Code
review traced it one layer deeper: a peer's automatic post-pull GC pass
(`Document.ApplyChangePack`'s `if !d.options.DisableGC && !hasSnapshot`
step) uses a stale `gcElementPairMap` entry left over from the original
`Remove`, and purges the *live* restored element by its reused
`createdAt` key. The two replicas do not reconverge — this is permanent
divergence, not a transient state. The lesson: when an undo/redo bug
reaches a peer via GC, "still shows the old value" and "will never show
the new value" look identical from a single assertion after one sync.
Chase the mechanism, not just the symptom, before writing off a
CRDT-adjacent bug as cosmetic.

## An identity-reuse bug family recurs across data types

This is structurally the same defect class as the duplicate-`TreeNodeID`
panic fixed 2026-08-14
(`docs/tasks/active/20260814-duplicate-tree-node-id-todo.md`): an
undo/redo path restores an element under its *original* identity rather
than a fresh one, and something keyed by that identity (there:
`NodeMapByID`; here: `Root.gcElementPairMap`/`elementMap`) ends up with
two claimants under one key, and the wrong one wins. The Tree fix added
`putNode` (keep the live node over a tombstone) and
`dropDuplicateContents`. The general shape — "does undo/redo ever
reinsert something under a `createdAt`/id it already used, and does
every map keyed by that identity know which occupant is authoritative?"
— is worth checking systematically across CRDT types rather than
patching each report as an unrelated one-off.

## A guard split across two SDKs by construction, not oversight

`set.go:102-104`'s `source == OpSourceUndoRedo` guard exists *because*
local undo/redo and remote application are supposed to behave
differently for ordinary operations (that asymmetry is Task 1's whole
`OpSource` design). It is easy to mistake a bug that only reproduces
under one `OpSource` for something introduced by the port, when in fact
JS has the byte-identical gate (`set_operation.ts:99-104`) and the
asymmetry is deliberate — it just doesn't cover this specific case (a
`Set`'s reverse reusing a `createdAt`, unlike `Add`/`ArraySet` which are
re-ticketed before redo). Confirming parity with JS before filing saved
this from being misdiagnosed as a Go regression; it is a shared design
gap that needs a coordinated fix in both SDKs, not a Go-only patch.

## Verify a suspected cross-SDK defect against JS source, not memory

Two of the three defects filed here were suspected from Go-side symptoms
alone; both were confirmed (or shown to have upstream tracking) only by
reading the actual JS source: `set_operation.ts` for the deregister gate,
`context.ts`/`presence.ts`/`util/object.ts` for the `undefined`-drops-via-
`JSON.stringify` mechanism, and `document.ts:2068-2069`'s existing
`TODO(chacha912)` for the `Presence.Initialize` staleness. Citing a line
range from memory or from the task brief's summary was not enough in any
of the three cases — the actual mechanism (JSON.stringify dropping
`undefined` keys, specifically) only became clear from reading
`util/object.ts` directly.

## "No production code changes" is a real constraint, not a formality

The instruction for this round was explicit: document three defects, add
one pinning test, touch zero lines of production logic. The natural next
move after tracing `set.go:102-104`'s asymmetry is to *fix* it — the fix
even looks small (drop one condition). Resisting that, and instead
writing the fix directions down for someone else's dedicated task, was
the right call per the port's own rule: a one-sided Go fix widens the
JS/Go gap this whole port exists to close. A traced, well-documented,
unfixed bug is a better outcome this round than an untested one-line fix
landed by the wrong task.
