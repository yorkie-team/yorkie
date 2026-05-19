**Created**: 2026-05-19

# Allowed Origins Wildcard

Branch: `feat-allowed-origins-wildcard`
Design: `docs/design/allowed-origins-wildcard.md`

## Goal

Support wildcard patterns in `Project.AllowedOrigins` so values like
`https://*.example.com` or `https://*-m.stock.example.com` match the
corresponding browser `Origin` headers instead of being silently dropped.

## Plan

- [ ] Add `server/rpc/interceptors/origin.go` with `matchOrigin(pattern,
      origin string) bool`
  - [ ] `"*"` matches anything
  - [ ] Exact-string fast path
  - [ ] URL parse, compare scheme (case-insensitive), host labels,
        port; reject if any of path/query/fragment is non-empty
  - [ ] Label-level glob via `path.Match` with case-insensitive
        comparison
- [ ] Add `server/rpc/interceptors/origin_test.go` table-driven tests
      covering each row of the examples table in the design doc plus
      negative cases (scheme/port/label-count/empty)
- [ ] Replace inner condition in `server/rpc/interceptors/yorkie.go`
      (`checkCORS`) with `matchOrigin(allowed, origin)`
- [ ] Tighten `valid_origin` in `api/types/updatable_project_fields.go`:
      reject wildcards outside host labels, reject empty labels, update
      translation string
- [ ] `make lint && make test`
- [ ] Commit (English, one commit for design + tests + impl unless
      review prefers split)
- [ ] Push and open PR

## Notes

- Keep behavior identical for patterns without `*` (exact equality).
- `path.Match` is used per-label after splitting on `.`; `.` cannot
  appear inside a label so cross-label leakage is impossible.
- No pre-compilation of patterns; the list is short and the match runs
  alongside MongoDB I/O.
