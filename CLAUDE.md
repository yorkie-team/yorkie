# Yorkie — CRDT-based Collaboration Server

Go server providing document synchronization via CRDTs with MongoDB or
in-memory storage.

Architecture docs live under `docs/design/`; tasks under @docs/tasks/README.md.

## Commands

```sh
make tools         # Install dev tools (run periodically)
make build         # Build binary to bin/yorkie
make fmt           # gofmt
make lint          # golangci-lint
make proto         # Regenerate protobuf via buf
make test          # Integration tests (-tags integration, MongoDB required)
make test-complex  # Long-running complex tests (-tags complex)
make bench         # Benchmarks (-tags bench)
make coverage      # Integration test coverage (MongoDB required)
go test ./...      # Unit-only tests (no build tag, no DB)

# Integration test environment
docker compose -f build/docker/docker-compose.yml up --build -d
```

## Commit Messages

Per `CONTRIBUTING.md`: subject ≤70 chars (what changed), blank line,
body wrapped at 80 chars (why). Enable the local commit-msg validator
once with `bash scripts/setup.sh`.

```text
Skip leadership write when active leader exists

Follower nodes unconditionally attempted FindOneAndUpdate with
is_leader=true every renewal cycle (default 5s). With N nodes this
produced N-1 duplicate key errors per cycle on the
is_leader_true_unique partial index.
```

In shell, use multiple `-m` flags or `$'...'` for real newlines —
not `\n` inside `"..."`.

## Key Architecture

- **Three-layer data model**: JSON-like (user-facing) → CRDT (conflict
  resolution) → Common (shared primitives).
- **Entry point**: `cmd/yorkie/`.
- **Generated code**: `api/yorkie/v1/` (protobuf via `make proto`).
- **Design docs**: `docs/design/` — always consult before changing
  architecture; keep updated when behavior changes.

## Pitfalls

- Build tags `integration`, `bench`, `complex` gate test files. The
  Makefile targets pass them via `go test -tags …`; for VSCode/gopls
  to index those files, set `gopls.build.buildFlags` per
  `CONTRIBUTING.md`.
- Apache 2.0 license header required on every Go file.
- Follow [Uber Go Style Guide](https://github.com/uber-go/guide/blob/master/style.md);
  every package needs a package comment (`// Package xxx provides…`).
- `protobuf` is generated — regenerate via `make proto`, don't hand-edit
  `api/yorkie/v1/*.pb.go`.
- No direct push to `main`; all changes via PR. CodeRabbit auto-reviews
  every PR.

## Task Workflow

Non-trivial tasks use paired files in `docs/tasks/active/` (flat):
`YYYYMMDD-<slug>-todo.md` and `YYYYMMDD-<slug>-lessons.md`. Each todo
starts with a `**Created**: YYYY-MM-DD` line — `scripts/tasks-archive.sh`
reads it to bucket into `docs/tasks/archive/YYYY/MM/`. Architecture
changes go to `docs/design/<topic>.md`.

1. **Plan** — write the todo file before touching code; update
   `docs/design/` if architecture changes.
2. **Branch + commit** — topic branch from `main`; each commit
   `make lint` green and `make test` green when MongoDB is up (or at
   least `go test ./...` for unit-only changes); follow the
   commit-message convention above.
3. **Self review** — dispatch `superpowers:requesting-code-review` (or
   `/code-review`) over the full branch diff before pushing. Apply
   blocking findings; note non-blocking as known limitations.
4. **Sync + open PR** — `git fetch && git rebase origin/main` to
   surface conflicts before pushing. Title ≤70 chars; body =
   Summary + Test plan. CodeRabbit will comment automatically.
5. **Address review** — evaluate each finding technically; push back
   with reasoning when wrong. Reply in the comment thread
   (`gh api repos/yorkie-team/yorkie/pulls/{pr}/comments/{id}/replies`),
   not top-level. If `main` moved during review, rebase again.
6. **Before merge** — CI green and maintainer approval: capture
   lessons in `*-lessons.md`, then
   `bash scripts/tasks-archive.sh && bash scripts/tasks-index.sh`
   to move the pair into `archive/YYYY/MM/` and regenerate the
   top-level and archive READMEs. The active README is hand-written;
   only touch it if the convention itself changed. Merge, then start
   a new session for the next task.
