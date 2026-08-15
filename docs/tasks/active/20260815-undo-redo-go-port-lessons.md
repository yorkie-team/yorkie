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

(Fill in as tasks complete.)

## Parity audit

(Fill in at Task 21: JS `it(` counts vs Go `t.Run(` counts per file.)
