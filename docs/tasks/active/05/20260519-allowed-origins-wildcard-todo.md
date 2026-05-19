**Created**: 2026-05-19

# Allowed Origins Wildcard

PR: https://github.com/yorkie-team/yorkie/pull/1810
Branch: `feat-allowed-origins-wildcard`
Design: `docs/design/allowed-origins-wildcard.md`

## Goal

Support wildcard patterns in `Project.AllowedOrigins` so values like
`https://*.example.com` or `https://*-m.stock.example.com` match the
corresponding browser `Origin` headers instead of being silently dropped.

## Done

- [x] Add `server/rpc/interceptors/origin.go` with `matchOrigin(pattern,
      origin string) bool`
  - [x] `"*"` matches anything
  - [x] Exact-string fast path (added after self-review)
  - [x] URL parse, compare scheme (case-insensitive), host labels,
        port; reject if any of path/query/fragment is non-empty
  - [x] Label-level glob via `path.Match` with case-insensitive
        comparison
- [x] Add `server/rpc/interceptors/origin_test.go` table-driven tests
      covering each row of the examples table in the design doc plus
      negative cases (scheme/port/label-count/empty)
- [x] Replace inner condition in `server/rpc/interceptors/yorkie.go`
      (`checkCORS`) with `matchOrigin(allowed, origin)`
- [x] Tighten `valid_origin` in `api/types/updatable_project_fields.go`:
      reject wildcards outside host labels, reject empty labels for all
      patterns, update translation string
- [x] `make lint && make test`
- [x] Two commits: design doc + implementation
- [x] Push and open PR (#1810)
- [x] Address CodeRabbit comment: hoist empty-label check above the
      wildcard-free early return

## Remaining

- [ ] PR review and merge

## Notes

- Keep behavior identical for patterns without `*` (exact equality).
- `path.Match` is used per-label after splitting on `.`; `.` cannot
  appear inside a label so cross-label leakage is impossible.
- No pre-compilation of patterns; the list is short and the match runs
  alongside MongoDB I/O.
