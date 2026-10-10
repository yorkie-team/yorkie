---
updated: 2026-10-10
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
| Stop the sync loop retrying a push the server will never accept (2026-10-10) | [20261010-park-rejected-writes-todo.md](./active/20261010-park-rejected-writes-todo.md) | [20261010-park-rejected-writes-lessons.md](./active/20261010-park-rejected-writes-lessons.md) |
| Watch stream heartbeat and SDK idle timeout (2026-10-08) | [20261008-watch-stream-heartbeat-todo.md](./active/20261008-watch-stream-heartbeat-todo.md) | [20261008-watch-stream-heartbeat-lessons.md](./active/20261008-watch-stream-heartbeat-lessons.md) |
| Skip auto revisions when only presence changed since last snapshot (2026-10-08) | [20261008-skip-auto-revision-presence-only-todo.md](./active/20261008-skip-auto-revision-presence-only-todo.md) | [20261008-skip-auto-revision-presence-only-lessons.md](./active/20261008-skip-auto-revision-presence-only-lessons.md) |
| Send presence as a patch on the way up (#2154) (2026-10-08) | [20261008-presence-patch-todo.md](./active/20261008-presence-patch-todo.md) | [20261008-presence-patch-lessons.md](./active/20261008-presence-patch-lessons.md) |
| Enforce the Auth Scheme an Admin Method Expects (2026-10-08) | [20261008-admin-auth-scheme-scope-todo.md](./active/20261008-admin-auth-scheme-scope-todo.md) | [20261008-admin-auth-scheme-scope-lessons.md](./active/20261008-admin-auth-scheme-scope-lessons.md) |
| Server-side gate for MaxSizePerDocument (2026-10-03) | [20261003-server-side-document-size-gate-todo.md](./active/20261003-server-side-document-size-gate-todo.md) | [20261003-server-side-document-size-gate-lessons.md](./active/20261003-server-side-document-size-gate-lessons.md) |
| Harden the advisory verbs (2026-09-26) | [20260926-harden-advisory-verbs-todo.md](./active/20260926-harden-advisory-verbs-todo.md) | [20260926-harden-advisory-verbs-lessons.md](./active/20260926-harden-advisory-verbs-lessons.md) |
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

- Archived task count: 79
- Archive index: [archive/README.md](./archive/README.md)
