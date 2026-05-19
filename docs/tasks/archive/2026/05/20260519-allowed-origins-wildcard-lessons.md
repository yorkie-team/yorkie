**Created**: 2026-05-19

# Allowed Origins Wildcard — Lessons

## Validation must agree with the matcher

The original `valid_origin` validator accepted any string that `url.Parse`
considered a valid URL. `https://*.example.com` parses fine, so the
dashboard stored it. The runtime check at `server/rpc/interceptors/yorkie.go`
then compared it with literal equality, so the pattern matched nothing.
A "silently stored, silently useless" pattern is worse than a write-time
error: the project owner discovers the bug only when every browser request
fails with `permission_denied`.

**Rule**: whenever the matcher rejects a class of input, the validator
must reject it too. Drift between the two is what produces silent
failures.

The CodeRabbit comment on this PR caught one of these drifts that the
self-review missed: the empty-label check was inside the wildcard-only
branch, so `https://.example.com` passed validation but never matched.

## `path.Match` semantics happen to fit DNS labels

`path.Match`'s `*` matches any sequence of non-`/` characters. DNS labels
cannot contain `/`, so after splitting the host on `.`, calling
`path.Match` per label gives exactly the "wildcard within a label" rule
we want. The treatment of `?` and `[...]` as metacharacters is fine here
because hostnames never legitimately contain them.

The alternative — running `path.Match` on the full host string — would
have let `*` cross `.` boundaries, turning `*.example.com` into a wildcard
that matches `a.b.example.com` and creating the usual `evil-example.com`
class of confusion.

## Wildcards inside a label, not just leftmost-label

The user's actual case was `https://*-m.stock.example.com` matching
`https://mini-m.stock.example.com`. The "subdomain-only" convention
(`*.example.com` where `*` is always a full leftmost label) cannot
express this. Label-internal glob is a strict superset and costs nothing
extra in implementation complexity, so it was the right call.

## Pre-compilation was tempting but not warranted

It is easy to imagine parsing each pattern once at project load and
caching the parsed form. In practice the list is short (typically <10),
the matcher runs alongside MongoDB I/O, and project objects do not have
a cache invalidation story. Pre-compilation would have meant adding a
new cache layer with invalidation logic to save a few microseconds per
request. YAGNI; the design doc explicitly chose not to do it.

## Test cases are easier to read as table rows

The matcher behavior is most naturally expressed as a table of
`(pattern, origin, want)` triples. Writing them as named subtests with
`t.Run` keeps each case independently identifiable in failure output and
avoids the "which assertion fired?" problem with a single mega-test.
