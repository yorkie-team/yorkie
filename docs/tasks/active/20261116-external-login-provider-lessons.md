# Add an external login provider verifying signed assertions — Lessons

**Created**: 2026-11-16

- Local users are inserted without an `auth_provider` field at all, not with
  an empty string. A unique index over `auth_provider` sees them as null, and
  a filter on `auth_provider: ""` never matches them. Check what the stored
  documents really look like before writing a filter or an index over them.
- A unique index added to a collection that older servers still write to
  breaks their inserts during a rolling update. A partial index over the new
  field leaves their documents out until the backfill reaches them.
- A backfill at start covers what older servers wrote before it, not what
  they write while both versions run. Backfill again lazily where the
  missing field is first needed.
- go-memdb's `CompoundIndex.AllowMissing` stops at the first missing field
  and still returns a key, so every local user got the same empty key in a
  "unique" index. `IndexSchema.AllowMissing` with a strict compound leaves
  them out instead.

## Self review

- Round 1, A (DB and backfill; one reviewer, 8 tool calls): nothing
  blocking. Fixed: users written by an older server after start were locked
  out of provider login (lazy backfill); the memory compound index gave all
  local users one key; `DeepCopy` no longer copies `AccessedAt`, which was an
  unrelated change. Added tests for the rolling-update case, racing
  subjects, and the default user's subject.
