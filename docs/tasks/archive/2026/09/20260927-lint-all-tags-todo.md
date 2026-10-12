**Created**: 2026-09-27

# Lint every build tag, and turn staticcheck and unused back on

Follow-up to #2063, which left two lint gaps as known limitations.

## Motivation

- `make lint` runs golangci-lint with no build tags, so the ~75 files behind
  `integration`, `bench`, `complex` and friends are never linted. Turning the
  tags on surfaces 177 findings there — 50 of them ignored errors (`errcheck`,
  `ineffassign`), which can hide a test that no longer checks what it claims.
- `staticcheck` and `unused` have been disabled since a 2022 tool bump
  (e52501d1), most likely because generics support lagged then. Enabled, they
  report 119 findings, including dead code and a value that is never used.

Measured together on linux/amd64 with every tag: 282 findings.

| linter | count | where |
|---|---|---|
| staticcheck | 105 | mostly `pkg/document` (QF1008 ×45, SA1019 ×13, QF1012 ×13, ST1023 ×9, QF1003 ×9, …) |
| lll | 78 | `test/{integration,bench,complex}` only |
| errcheck | 40 | `test/{integration,bench}` |
| unused | 14 | `server/rpc`, `test/integration`, `pkg/document`, … |
| goconst | 14 | `test/integration` |
| ineffassign | 12 | `test/{integration,bench}` |
| goimports | 8 | tag-gated tests |
| wrapcheck, gosec | 4 + 4 | tag-gated tests |
| gocyclo | 3 | `test/integration` |

## Plan

- [x] Share the tag derivation: `scripts/go-build-tags.sh` prints the tag set
      read off `//go:build`; `scripts/go-fix.sh` and `make lint` both use it,
      on linux/amd64.
- [x] Exclude `lll`, `goconst`, `gocyclo` in the tag-gated suites under
      `test/` (`integration`, `bench`, `complex`, `fuzz`) — golden strings,
      repeated fixture keys and table-driven tests are the norm there; every
      finding of the three is in them. `test/helper` stays linted.
- [x] Fix `errcheck` and `ineffassign`; a test that fails once its error is
      checked is a finding in its own right, not something to suppress.
- [x] Fix `goimports`, `wrapcheck`; `gosec` G404 on seeded test generators
      gets a `//nolint:gosec` with the reason.
- [x] Enable `staticcheck`: fix SA/S/QF/ST findings; SA1019 on the deprecated
      Watch RPCs kept for old clients is excluded in `.golangci.yml` for
      `server/rpc/yorkie_server.go` only, with the reason; QF1008 is off.
- [x] Enable `unused`: delete dead code.
- [x] Update the linter inventories: CLAUDE.md pitfalls,
      agent-command-verbs.md §4b, `MECHANICAL_COVERAGE_NOTE`.

## Verification

- [x] `make lint` clean with the new config
- [x] `make verify`
- [x] Integration tests for every file whose errors are now checked, against
      local MongoDB
- [x] Bench compile + `-benchtime=2x` for touched benchmarks
- [x] `node --test scripts/test/*.test.mjs scripts/agent/*.test.mjs`
- [x] CI
      *Audit 2026-10-05:* #2067 (35e145ce) merged with CI green.

## Review

Lint is at 0 issues under every tag with staticcheck and unused on.
Turning on the error checks surfaced real test defects rather than noise —
see the lessons file:

- Two integration tests decoded admin REST responses into the wrong type and
  had never checked the error; they passed on map keys alone.
- Two tests asserted nothing their names or comments promised.

Verified locally: `make verify`, the integration suite (native, plus the
amd64-only tests under Rosetta), every benchmark at 2x, and the complex lane.
`TestClientWithShardedDB/FindDocInfoByRefKey_with_duplicate_ID_test` fails
locally on this branch and on main alike — it needs a sharded MongoDB, which
local runs do not have; CI's complex lane runs one.
