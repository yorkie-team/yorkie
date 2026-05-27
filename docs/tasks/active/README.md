---
updated: 2026-05-26
---

# Active Tasks

In-progress task files live here.

- Todo/review: `YYYYMMDD-<slug>-todo.md`
- Lessons: `YYYYMMDD-<slug>-lessons.md`

Each todo should open with a `**Created**: YYYY-MM-DD` line — both
the archive script and the index script read it (with a filename
fallback) to bucket and date entries.

When work is complete:

```sh
bash scripts/tasks-archive.sh  # moves the pair into archive/YYYY/MM/
bash scripts/tasks-index.sh    # regenerates ../README.md and ../archive/README.md
```
