# Lessons: Enforce the presence JSON wire contract on the server

**Created**: 2026-11-18

## Notes

- `converter.FromPresenceChange` sits on two paths with opposite requirements,
  exactly like `FromOperations`: the wire, where rejecting malformed input is
  the point, and `database.PresenceChangeFromBytes`, where rejecting anything
  makes the document holding that row unloadable. The repo already had the
  shape for this — `api/converter/normalize.go` — so the presence fix is a twin
  of `FromStoredOperations`, not a new idea.
- The faithful repair for a stored non-JSON presence value is to JSON-encode
  it. Go readers already saw the raw text (`DecodeValue` returns a non-JSON
  value as it arrived), and after the repair they still do; JS readers go from
  a thrown `JSON.parse` to the same text. No reader loses anything.
- Because the fan-out path (`ServerPack.ToPBChangePack`) re-encodes
  `info.PresenceChange`, which came from `PresenceChangeFromBytes`, repairing
  at the stored-decode step is enough to keep the strict client-side decode
  from failing on legacy rows. No separate `Sanitize…` twin was needed.

## Self review

Not run: this task was executed by the autonomous agent workflow, which is
granted no tool that can dispatch the reviewer subagent. CI, `@claude review`
and a human reviewer stand in. Recorded here rather than left implicit so a
skipped round does not read as a clean one.
