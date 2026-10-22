# Lessons — Reserve dead proto fields

## What the audit got right, and the one thing it understated

The issue listed `JSONElementSimple.moved_at`/`removed_at` as unproduced. That
is true, but the *similar-looking* fields on `JSONElement` are load-bearing:
`to_bytes.go:129` writes `obj.MovedAt()` and `from_bytes.go:186` reads it back
into `SetMovedAt`, which is exactly what `crdt.PositionedAt` later consults.
Reserving by name on the wrong message would have broken snapshot rebuild. The
two messages are only distinguishable by reading which one each converter path
constructs — grepping for `MovedAt` alone cannot tell them apart.

## A deprecated field can still be a test's only fixture

`Operation.Edit.created_at_map_by_actor` was the only
`map<string, TimeTicket>` left in the schema, and
`api/converter/reissue_test.go` used it as the fixture for
`ticketReissuer.rekeyMap`. Reserving it took the fixture with it. The useful
observation is that `rekeyMap` fires on *every* string-keyed map, so the
behaviour under test was never specific to that field: retargeting the test at
`VersionVector.vector` — which is the map the reissuer actually meets in
production, and which can hold both the hex and the base64 key form in one
message — covers strictly more than the original did.

## Verify the generator is reproducible before trusting its diff

`make proto` was run once on a clean tree before any edit. It produced no diff,
which is what makes the post-edit regeneration safe to attribute entirely to
the `reserved` statements. Without that baseline, a plugin-version drift would
be indistinguishable from the intended change inside a 4k-line generated diff.

## Self review

Not run. `/self-review` dispatches a reviewer subagent and this autonomous run
was granted no tool that can do so; recording the gap rather than letting a
skipped round read as a clean one. Review falls to CI, `@claude review`, and a
human.
