---
updated: 2026-10-29
---

# Tasks Index

Track task-specific plan/review and lessons files using the active/archive layout.

## Layout

- Active tasks: [`docs/tasks/active/`](./active/README.md)
- Archived tasks: [`docs/tasks/archive/YYYY/MM/`](./archive/README.md)

## Naming

- Todo/review: `YYYYMMDD-<slug>-todo.md`
- Lessons: `YYYYMMDD-<slug>-lessons.md`

## Active Tasks

| Task | Todo | Lessons |
|---|---|---|
| Make the lazy ticket and node-ID key caches race-free (2026-10-29) | [20261029-ticket-key-atomic-cache-todo.md](./active/20261029-ticket-key-atomic-cache-todo.md) | [20261029-ticket-key-atomic-cache-lessons.md](./active/20261029-ticket-key-atomic-cache-lessons.md) |
| `version --output json` mixes the server-fetch error into stdout (2026-10-25) | [20261025-version-server-error-to-stderr-todo.md](./active/20261025-version-server-error-to-stderr-todo.md) | [20261025-version-server-error-to-stderr-lessons.md](./active/20261025-version-server-error-to-stderr-lessons.md) |
| Ticket.Key caches its key without synchronization (2026-10-25) | [20261025-ticket-key-data-race-todo.md](./active/20261025-ticket-key-data-race-todo.md) | [20261025-ticket-key-data-race-lessons.md](./active/20261025-ticket-key-data-race-lessons.md) |
| Pin how a reader decodes CHANGE_TYPE_UNSPECIFIED presence (2026-10-25) | [20261025-presence-unspecified-decode-test-todo.md](./active/20261025-presence-unspecified-decode-test-todo.md) | [20261025-presence-unspecified-decode-test-lessons.md](./active/20261025-presence-unspecified-decode-test-lessons.md) |
| The node GC worklist has no guard for a child whose removal was cleared (2026-10-25) | [20261025-gc-node-worklist-live-guard-todo.md](./active/20261025-gc-node-worklist-live-guard-todo.md) | [20261025-gc-node-worklist-live-guard-lessons.md](./active/20261025-gc-node-worklist-live-guard-lessons.md) |
| Read the sessions summary through rl_session_daily by name (2026-10-24) | [20261024-session-summary-rollup-hint-todo.md](./active/20261024-session-summary-rollup-hint-todo.md) | [20261024-session-summary-rollup-hint-lessons.md](./active/20261024-session-summary-rollup-hint-lessons.md) |
| Deprecate dead proto fields and correct the MovedAt deprecation plan (2026-10-22) | [20261022-reserve-dead-proto-fields-todo.md](./active/20261022-reserve-dead-proto-fields-todo.md) | [20261022-reserve-dead-proto-fields-lessons.md](./active/20261022-reserve-dead-proto-fields-lessons.md) |
| Remove the MCP server (2026-10-22) | [20261022-remove-mcp-server-todo.md](./active/20261022-remove-mcp-server-todo.md) | [20261022-remove-mcp-server-lessons.md](./active/20261022-remove-mcp-server-lessons.md) |
| Redo after GC drops the attributes of re-inserted text (2026-10-22) | [20261022-redo-drops-reinserted-text-attributes-todo.md](./active/20261022-redo-drops-reinserted-text-attributes-todo.md) | [20261022-redo-drops-reinserted-text-attributes-lessons.md](./active/20261022-redo-drops-reinserted-text-attributes-lessons.md) |
| Go presence values are not JSON-encoded, so JS peers fail to decode them (2026-10-22) | [20261022-presence-json-value-encoding-todo.md](./active/20261022-presence-json-value-encoding-todo.md) | [20261022-presence-json-value-encoding-lessons.md](./active/20261022-presence-json-value-encoding-lessons.md) |
| Dedup counter HLL: bound restored registers, pin the hash across SDKs (2026-10-22) | [20261022-hll-register-bound-and-hash-vectors-todo.md](./active/20261022-hll-register-bound-and-hash-vectors-todo.md) | [20261022-hll-register-bound-and-hash-vectors-lessons.md](./active/20261022-hll-register-bound-and-hash-vectors-lessons.md) |
| A peer's Tree undo leaves pending undo entries on stale indices (2026-10-13) | [20261013-remote-identity-undo-reconcile-todo.md](./active/20261013-remote-identity-undo-reconcile-todo.md) | [20261013-remote-identity-undo-reconcile-lessons.md](./active/20261013-remote-identity-undo-reconcile-lessons.md) |
| Merge-moved children sit in arrival order (2026-09-24) | [20260924-merge-moved-child-order-todo.md](./active/20260924-merge-moved-child-order-todo.md) | - |
| Install the `@claude` command surface, in four phases (2026-09-22) | [20260922-agent-command-verbs-todo.md](./active/20260922-agent-command-verbs-todo.md) | [20260922-agent-command-verbs-lessons.md](./active/20260922-agent-command-verbs-lessons.md) |
| Undoing a container removal discards a peer's concurrent edit inside it (2026-09-12) | [20260912-undo-discards-concurrent-peer-edit-todo.md](./active/20260912-undo-discards-concurrent-peer-edit-todo.md) | - |
| Collection changes where a later insert lands (2026-09-12) | [20260912-collection-changes-rga-insertion-todo.md](./active/20260912-collection-changes-rga-insertion-todo.md) | - |
| Applying one change twice corrupts an object's createdAt index (2026-09-11) | [20260911-duplicate-change-application-not-idempotent-todo.md](./active/20260911-duplicate-change-application-not-idempotent-todo.md) | - |
| Project Stats Long-Retention Windows Implementation Plan (2026-08-31) | [20260831-project-stats-long-retention-todo.md](./active/20260831-project-stats-long-retention-todo.md) | [20260831-project-stats-long-retention-lessons.md](./active/20260831-project-stats-long-retention-lessons.md) |
| DocSize: a snapshot rebuild over-credits one ticket per tombstone (2026-08-17) | [20260817-docsize-snapshot-rebuild-drift-todo.md](./active/20260817-docsize-snapshot-rebuild-drift-todo.md) | [20260817-docsize-snapshot-rebuild-drift-lessons.md](./active/20260817-docsize-snapshot-rebuild-drift-lessons.md) |
| Tree Style: a combined reverse's removal half is dropped on execute (2026-08-16) | [20260816-tree-style-combined-reverse-dropped-todo.md](./active/20260816-tree-style-combined-reverse-dropped-todo.md) | [20260816-tree-style-combined-reverse-dropped-lessons.md](./active/20260816-tree-style-combined-reverse-dropped-lessons.md) |
| Tree: a splitting edit that also inserts or removes loses its undo entry (2026-08-16) | [20260816-tree-split-edit-loses-undo-entry-todo.md](./active/20260816-tree-split-edit-loses-undo-entry-todo.md) | [20260816-tree-split-edit-loses-undo-entry-lessons.md](./active/20260816-tree-split-edit-loses-undo-entry-lessons.md) |
| Remote redo of a restored key can delete it on a peer (2026-08-16) | [20260816-remote-redo-replica-divergence-todo.md](./active/20260816-remote-redo-replica-divergence-todo.md) | [20260816-remote-redo-replica-divergence-lessons.md](./active/20260816-remote-redo-replica-divergence-lessons.md) |

## Archive

- Archived task count: 100
- Archive index: [archive/README.md](./archive/README.md)
