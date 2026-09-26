**Created**: 2026-09-26

# Catch the codebase up to Go 1.26 and keep it there

The toolchain moved to 1.26 in #2059, and #2058/#2060 adopted a first set of
modernizations. A full sweep with the 1.26 `go fix` across every build tag
still reports 61 files, and a handful of idioms from 1.19–1.25 that `go fix`
does not rewrite are still hand-rolled.

## Motivation

The gap has a structural cause, not just a missed grep:

- `golangci-lint` runs with no build tags, so the ~75 files behind
  `integration`, `bench`, `complex` and friends are never linted.
- #2058 was written against a 1.25 toolchain, so it could only grep for what
  the 1.26 `go fix` would report, and missed sites.
- Nothing in `make verify` fails when a modernizer has something to say, so the
  next toolchain bump will drift the same way.

## Plan

PR 1 (this task) — code and gate. One commit per intent:

- [x] `go fix` across all tags on linux/amd64, to a fixed point (two passes).
- [x] `parseSimpleXML` in `test/complex`: restore the three-clause loop that
      #1348 broke by hand (found while reviewing the `go fix` diff).
- [x] Typed atomics everywhere (production and tests); `wg.Go` for the two
      remaining `wg.Add(1)` + `go func(args)`; `b.Loop` for all 16 `b.N` loops.
- [x] `sort` → `slices`; the six collect-then-sort loops use
      `slices.AppendSeq` onto a sized slice, then `slices.Sort`.
- [x] Gate: `make verify-modernize` in `make verify` and as a CI lane;
      `make modernize` to apply.
- [ ] Lint every build tag, and `staticcheck` — pending a scope decision:
      159 findings in tag-gated tests, 96 from staticcheck/unused.

PR 2 (separate) — Dockerfile runtime image off EOL `debian:buster-slim` /
`alpine:3.19`.

Dropped: `math/rand` → `math/rand/v2`. Every use is a seeded
`rand.New(rand.NewSource(seed))` in tests or benchmarks; v2 generators give a
different sequence per seed, which would invalidate recorded "seed N
diverged" reproductions and benchmark history for no runtime gain.

Out of scope: native h2c (needs a reproduction of the shutdown behaviour
first), `t.Context()` (it is cancelled before `t.Cleanup` runs, so each of the
84 test files needs its own judgement).

## Verification

- [x] `go build ./...` and `go vet` with every tag
- [x] `make lint`, `make verify-modernize` (red on a planted loop and on a
      compile error, green after `make modernize`), `go test ./...`
- [x] `-race` over every package with converted atomics; integration
      Webhook/Document/Housekeeping/Revision/Schema/Channel/Cluster runs and
      `./server/backend/database/...` against local MongoDB
- [x] `test/complex` tree lane: 1610 pass before the `parseSimpleXML` fix,
      1611 after (the new test included)
- [x] `node --test scripts/test/*.test.mjs`, doc links, licence headers
- [ ] `make test` / integration lanes via CI

## Review

Three self-review rounds (see the lessons file). Beyond the doc fixes they
caught two real defects in this branch: `make modernize` failing open on a
tree that did not compile, and `BenchmarkPresenceConcurrency` dying under
`b.Loop` with its timer stopped. Every benchmark was then run at
`-benchtime=2x` (266, all passing).

Known limitations, carried to the PR body:

- `parseSimpleXML` still mis-tokenizes non-ASCII text and panics on an
  unterminated `<`; every input is a fixed ASCII literal.
- Lint still skips the tag-gated files, and `staticcheck`/`unused` stay off —
  159 and 96 findings respectively, left for a follow-up PR.
