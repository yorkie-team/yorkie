# Lessons: Enforce the presence JSON wire contract on the server

**Created**: 2026-11-18

## Notes

- The first cut copied the `FromOperations`/`FromStoredOperations` split:
  strict on the wire, lenient on the stored path. That split works for
  operations because the two paths really are separate functions with separate
  callers. Presence has three callers, not two, and the third breaks the
  analogy: `FromPresenceChange` is also the Go *client's* pull decoder
  (`client/client.go` Attach/Detach/PushPullChanges). A rejection there
  protects nobody and wedges the client, and a rejection at ingress refuses the
  entire change pack — every batched document operation with it — of any client
  running a Go SDK older than `presence.EncodeValue` (0.6.36), permanently and
  with no recourse but an upgrade.
- So the enforcement became a repair on every path instead of a rejection on
  one: `repairPresence` JSON-encodes, in place, any value peers cannot parse.
  It keeps the invariant the task was filed for — nothing unparseable is ever
  persisted or fanned out — while costing no version-skew story at all. Worth
  remembering before reaching for a strict/lenient pair: count the callers
  first, because the pair only pays off when the strict side has a caller that
  can actually act on the rejection.
- The faithful repair for a non-JSON presence value is to JSON-encode it. Go
  readers already saw the raw text (`DecodeValue` returns an unencoded value as
  it arrived), and after the repair they still do; JS readers go from a thrown
  `JSON.parse` to the same text. No reader loses anything.
- `json.Valid` is a weaker predicate than "a JS peer can `JSON.parse` it": Go's
  scanner accepts nesting up to 10000 levels, while V8 throws `RangeError` from
  a stack overflow well before that. A syntax check alone would have left the
  wedge the task was filed to close half open, so the repair also covers
  anything nested past `maxPresenceValueDepth`.
- Because the fan-out path (`ServerPack.ToPBChangePack`) re-encodes
  `info.PresenceChange`, which came from `PresenceChangeFromBytes`, repairing
  at the decode step is enough to keep a pulling client from seeing a legacy
  value. No separate `Sanitize…` twin was needed.

- The integration lane caught what unit tests could not: several suites build
  change packs straight from `api.Presence` / `inner.Presence` literals rather
  than through a document's write path, so they carried bare strings
  (`"name": "a"`) that the new boundary rewrites. Those fixtures were
  JSON-encoded so their assertions describe the fold rather than the repair.
  They are the only in-tree clients that bypass
  `presence.Set`, which is the evidence that the SDK write paths already
  satisfy the contract — and the reminder that a wire-level check has to be
  validated against the raw-RPC tests, not only against the SDK ones.
- One cosmetic consequence: `packs.validateClientSeqContinuity` dereferences
  the pack it is handed, so a test that `assert.NoError`s a failed
  `FromChangePack` and keeps going panics on the nil pack instead of stopping
  at the assertion. The panic was the fixture's, not the server's; hardening
  that signature is outside this change.

## Self review

Not run: this task was executed by the autonomous agent workflow, which is
granted no tool that can dispatch the reviewer subagent. CI, `@claude review`
and a human reviewer stand in. Recorded here rather than left implicit so a
skipped round does not read as a clean one.
