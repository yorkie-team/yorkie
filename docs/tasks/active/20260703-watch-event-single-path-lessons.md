# Lessons: watch event single path

**Created**: 2026-07-03

(Learnings captured during the task; finalized before archive.)

- The `ReconcilePresence` state machine (0.7.6) unified event
  *decisions* but left event *delivery* dual-pathed on the Go client —
  a reconciliation design needs to cover both to be race-free.
- "Same pattern as applyChanges" was a false safety argument for
  sending on the buffer-1 event channel under `d.mu`: applyChanges is
  driven by the app's own Sync call, while reconcile emission is
  driven externally by the watch stream. An external producer blocking
  under the document mutex forms a wait cycle with any consumer that
  reenters the document (`Update`/`Sync`) while handling events.
  Deadlock analysis must trace the full consumer chain, not just the
  producer side.
- Delivery pipelines need explicit ownership rules: exactly one
  consumer per channel (`d.Events()` → pump), exactly one
  writer/closer per channel (sender → `rch`), and teardown ordered so
  producers always outlive their consumer (stream reader exits before
  pump stops). The pre-existing reconnect path violated all three.
- Backpressure from a user-facing channel must be absorbed by an
  unbounded buffer before it reaches any lock-holding producer.
- Removing backpressure changes timing that tests silently rely on:
  the unthrottled stream reader processed unwatch before the
  observer's sync applied the peer's presence, so the reconcile state
  machine (correctly, JS-parity) emitted nothing and the test hung on
  its wait group. The goroutine dump made this obvious — always grab
  the dump instead of guessing at a hang. Tests asserting join/leave
  pairs must observe the join before triggering the leave.
