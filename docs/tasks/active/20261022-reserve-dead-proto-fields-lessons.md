# Lessons — Deprecate dead proto fields

## `reserved` is not a compatible way to retire a field under buf's FILE category

The obvious encoding of "nothing uses this number any more" is `reserved N;`,
and protobuf itself treats that as wire-compatible. buf's `FILE` breaking
category does not: `FIELD_NO_DELETE` compares field sets and fires on a missing
number whether or not it was reserved. The reserved-aware rule is
`FIELD_NO_DELETE_UNLESS_NUMBER_RESERVED`, and only `WIRE` and `WIRE_JSON` use
it. So under `breaking.use: [FILE]` the strongest in-file statement available
is `[deprecated = true]`, which keeps the number declared and still propagates
a `// Deprecated:` marker into generated code. Check the configured category
before planning a reservation — the two read as interchangeable and are not.

## What the audit got right, and the one thing it understated

The issue listed `JSONElementSimple.moved_at`/`removed_at` as unproduced. That
is true, but the *similar-looking* fields on `JSONElement` are load-bearing:
`to_bytes.go:129` writes `obj.MovedAt()` and `from_bytes.go:186` reads it back
into `SetMovedAt`, which is exactly what `crdt.PositionedAt` later consults.
Marking the wrong message's fields would have mislabelled snapshot rebuild. The
two messages are only distinguishable by reading which one each converter path
constructs — grepping for `MovedAt` alone cannot tell them apart.

## A deprecated field can still be a test's only fixture

`Operation.Edit.created_at_map_by_actor` was the only
`map<string, TimeTicket>` left in the schema, and
`api/converter/reissue_test.go` used it as the fixture for
`ticketReissuer.rekeyMap`. Deprecating it makes that fixture a deprecated-API
use in the repository's own tests, which is the thing the marker exists to
discourage. The useful
observation is that `rekeyMap` fires on *every* string-keyed map, so the
behaviour under test was never specific to that field: retargeting the test at
`VersionVector.vector` — which is the map the reissuer actually meets in
production, and which can hold both the hex and the base64 key form in one
message — covers strictly more than the original did.

## Verify the generator is reproducible before trusting its diff

`make proto` was run once on a clean tree before any edit. It produced no diff,
which is what makes the post-edit regeneration safe to attribute entirely to
the field markers. Without that baseline, a plugin-version drift would
be indistinguishable from the intended change inside a 4k-line generated diff.

## Self review

Not run. `/self-review` dispatches a reviewer subagent and this autonomous run
was granted no tool that can do so; recording the gap rather than letting a
skipped round read as a clean one. Review falls to CI, `@claude review`, and a
human.
