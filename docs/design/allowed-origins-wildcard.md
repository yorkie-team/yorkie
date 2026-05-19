---
title: allowed-origins-wildcard
target-version: 0.7.10
---

# Allowed Origins Wildcard

## Problem

`Project.AllowedOrigins` is documented as "the list of allowed origins" and
the dashboard UI lets users enter values such as `https://*.example.com`.
However, the server treats every entry as a literal string. The check in
`server/rpc/interceptors/yorkie.go` is:

```go
for _, allowed := range project.AllowedOrigins {
    if allowed == "*" || allowed == origin {
        return nil
    }
}
```

The only wildcard understood by the server is the bare `"*"` (allow every
origin). Patterns like `https://*.example.com` are accepted by the
`valid_origin` validator because Go's URL parser is permissive, but they
never match any real `Origin` header, so the project silently rejects every
browser request.

This is a silent failure: writes succeed, no error is surfaced, and a
project owner only notices when SDK clients fail with `permission_denied`.

### Goals

- Support wildcard patterns in `AllowedOrigins` so a project owner can write
  one entry that matches a set of subdomains (e.g. `https://*.example.com`,
  `https://*-m.stock.example.com`).
- Keep `"*"` as the universal "allow any origin" shortcut.
- Reject patterns at write time that put wildcards in positions where the
  server cannot match them (scheme, port, path).

### Non-Goals

- Regex-based matching. A small glob is enough for the use cases above and
  keeps the surface easy to reason about.
- Cross-label matching such as `*.example.com` matching
  `a.b.example.com`. Restricting `*` to a single DNS label keeps the rule
  predictable and avoids the common "evil-example.com" footguns.
- Pre-compiling patterns into a cached form. The allowed-origins list is
  short (typically <10 entries) and the match runs once per RPC. The cost is
  dominated by surrounding work (MongoDB lookup, webhook calls).

## Design

### Matching rule

`matchOrigin(pattern, origin string) bool` returns `true` when `origin` is
allowed by `pattern`.

1. If `pattern == "*"`, return `true`.
2. If `pattern == origin` (exact match), return `true`. This is the fast
   path for patterns without wildcards.
3. Otherwise parse both as URLs and compare component by component:
   - `scheme`: exact, case-insensitive match.
   - `host` (without port): split on `.`, the label counts must be equal,
     and each label is matched with `*` meaning "any sequence of
     characters not containing `.`". Label comparison is case-insensitive.
   - `port`: exact match. Missing port on both sides is treated as a match.
     Default port resolution (`https`/`443`, `http`/`80`) is **not** done:
     if the user wrote a port in either side, the other side must match
     literally. Browsers do not include default ports in the `Origin`
     header, and the stored pattern reflects what the user typed, so this
     mirrors the existing exact-match behavior.
   - `path`/`query`/`fragment`: must be empty on both sides. The `Origin`
     header never carries these; a pattern that does is a validation error.

Label-level glob is implemented with `path.Match` after replacing the
pattern label and target label individually. `path.Match` treats `*` as
"any sequence not containing the path separator `/`", which is fine
because DNS labels cannot contain `/`. Within a single label, `*` therefore
matches any text (including the empty string), and `.` cannot appear
because we already split on `.`.

### Examples

| Pattern | Origin | Match | Note |
|---------|--------|-------|------|
| `*` | `https://foo.com` | yes | Universal wildcard (existing). |
| `https://foo.com` | `https://foo.com` | yes | Exact match (existing). |
| `https://*.example.com` | `https://api.example.com` | yes | Single-label wildcard. |
| `https://*.example.com` | `https://a.b.example.com` | no | `*` cannot cross `.`. |
| `https://*m.stock.example.com` | `https://mini-m.stock.example.com` | yes | Wildcard inside a label. |
| `https://api-*.example.com` | `https://api-prod.example.com` | yes | Wildcard at end of label. |
| `https://foo.com` | `http://foo.com` | no | Scheme mismatch. |
| `https://foo.com:8080` | `https://foo.com` | no | Port mismatch. |
| `https://*.com` | `https://anything.com` | yes | Allowed: project owner's call. |

### Validation

The `valid_origin` validator is updated to:

1. Accept `"*"`.
2. Otherwise, require that the value parses as an absolute URL with a
   non-empty scheme and host.
3. If the value contains `*`, require that wildcards appear only in host
   labels. Reject wildcards in scheme, port, path, query, fragment.
4. Reject host patterns whose labels are empty after splitting (e.g.
   `https://.example.com`).

Patterns that pass validation but contain no wildcards keep their original
behavior of exact-string match.

### Components

- `server/rpc/interceptors/origin.go` (new): `matchOrigin(pattern, origin
  string) bool` plus a small helper for label-level glob.
- `server/rpc/interceptors/origin_test.go` (new): table-driven tests for
  every row in the examples table plus negative cases (scheme, port,
  label-count, empty pattern).
- `server/rpc/interceptors/yorkie.go`: replace the inner condition in
  `checkCORS` with `matchOrigin(allowed, origin)`.
- `api/types/updatable_project_fields.go`: tighten the `valid_origin`
  validator as described above. Update the translation string to mention
  wildcard support.

### Risks and Mitigation

| Risk | Mitigation |
|------|------------|
| Existing tighter exact-match origins (no `*`) start matching extra origins. | They don't. Step 2 of the matching rule is exact string equality; patterns without wildcards behave the same as today. |
| Project owners write overly-broad patterns (`https://*.com`). | This is the project owner's responsibility, the same as writing `*` is today. Documented in the dashboard help text. |
| `path.Match` returns a syntax error for malformed labels. | A `path.Match` error is treated as "no match" rather than propagating. Validation already rejects unbalanced brackets at write time. |
| Browsers send the origin with an unexpected port. | Behavior matches today: missing-vs-present port is a mismatch. Owners who need explicit ports continue to specify them. |

### Design Decisions

| Decision | Reason |
|----------|--------|
| `*` matches within a single DNS label only. | Predictable scope; prevents `*.example.com` from accidentally matching `a.b.example.com` or, with another implementation, `evil-example.com`. |
| Exact-match fast path before URL parsing. | Avoids `url.Parse` overhead on the common case where the project lists literal origins. |
| No default-port resolution. | The current behavior already does literal comparison; users who care about ports specify them, and `Origin` headers do not carry default ports. |
| No pattern pre-compilation. | The list is tiny and the match is cheap; pre-compilation adds cache-invalidation complexity for no measurable win. |
| Implement glob with `path.Match`. | Standard library, well-tested, semantics match the requirement after splitting on `.`. |

## Alternatives Considered

| Alternative | Why not |
|-------------|---------|
| Subdomain-only wildcard (`*.example.com`, `*` is always a full leftmost label). | Cannot express the dashboard user's actual case `*m.stock.example.com`. The label-internal glob is a strict superset. |
| Generic glob (`path.Match` against the full origin string, including `.`). | `*` would cross `.` boundaries, making `*.example.com` match `a.b.example.com`. Harder to reason about; security footgun. |
| Regex patterns. | Overkill for the use case, easy to write a pattern that hangs the matcher, more surface for confusion. |
| Pre-compile patterns into structured form, cached on the project. | Adds invalidation work on every project update and saves microseconds on a path that already does I/O. |

## Tasks

Track execution plans in `docs/tasks/active/` as separate task documents.
