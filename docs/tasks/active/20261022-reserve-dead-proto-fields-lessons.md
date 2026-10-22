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

Measured rather than inferred, with the buf the CI job pins (1.50.0, see
`.github/workflows/ci.yml`), against this branch's own base:

```text
$ buf config ls-breaking-rules --version v1 | grep FIELD_NO_DELETE
FIELD_NO_DELETE                         FILE, PACKAGE
FIELD_NO_DELETE_UNLESS_NAME_RESERVED    WIRE_JSON
FIELD_NO_DELETE_UNLESS_NUMBER_RESERVED  WIRE_JSON, WIRE

# with ChangePack.min_synced_ticket = 5 replaced by `reserved 5;` plus
# `reserved "min_synced_ticket";`
$ buf breaking api --against '.git#ref=origin/main,subdir=api'
api/yorkie/v1/resources.proto:42:1:Previously present field "5" with name
"min_synced_ticket" on message "ChangePack" was deleted.   # exit 100

# with `[deprecated = true]`, as shipped
$ buf breaking api --against '.git#ref=origin/main,subdir=api'   # exit 0
```

Reserving by number *and* name — the pair protobuf itself treats as the
compatible retirement — is what trips the rule; `buf breaking` cannot stay
green and the numbers be reserved at the same time while `FILE` is the
configured category. Making both true needs `api/buf.yaml` to move to
`WIRE_JSON` or to list `FIELD_NO_DELETE` under `breaking.except`, which
relaxes the repository's compatibility gate for every future proto change,
not just these seven slots.

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
