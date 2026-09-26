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
make verify        # lint + licence headers + go fix + unit tests — the commit gate
make modernize     # Apply go fix under every build tag (fixes verify-modernize)
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
body wrapped at 80 chars (why). `bash scripts/setup.sh`, once per clone,
installs the hooks that check this and the gates in step 2 below.

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
   `make verify` green, plus `make test` when MongoDB is up and the
   change reaches the integration lane; follow the commit-message
   convention above. `bash scripts/setup.sh` installs hooks that check
   part of this for you — `make lint` on commit, the full `make verify`
   on push — so the per-commit half is lint only and the tests are
   caught one layer out, not per commit.
3. **Self review** — `/self-review`: a bounded loop of review → fix →
   re-verify over the full branch diff, **max 3 rounds, stopping at the
   first round with no blocking findings**. Rotate what you weight per
   round (1 correctness/tests, 2 design fit, 3 security/docs) — the same
   reviewer asked three times mostly restates itself. When a reviewer
   cannot be launched, say so and ask; never let a skipped round read as
   a clean one. Log each round in `*-lessons.md`. A finding you believe
   is wrong goes there with evidence; one merely ignored is re-raised
   every round. Non-blocking findings become known limitations in the PR
   body.
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
