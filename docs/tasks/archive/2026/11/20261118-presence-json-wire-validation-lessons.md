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

## Review round: bounding the repair

- The repair is a rewrite, and a rewrite that expands is an amplifier. JSON
  encoding is sixfold on its worst input (a byte below `0x20` becomes
  `\u00XX`), protobuf carries those bytes in a `string` field, and presence is
  excluded from every document size gate — so the repair, left unbounded, let
  one push cost several times its own bytes in memory, in storage and again in
  the fan-out. The guard is a per-presence budget on *growth*
  (`maxPresenceRepairGrowth`): a legacy bare string grows by two quotes
  however long it is, so the bound only ever bites the shape that amplifies.
- Budgeting across a map needs an order, or two servers decoding the same
  bytes disagree about which values the budget reached. Keys are sorted before
  the budget is spent; Go's randomized map order would otherwise have made the
  decode non-deterministic.
- `presenceEncodeGrowth` predicts the encoder instead of calling it, so a
  value the budget cannot afford is never built — which only helps if the
  prediction is exact, hence the internal test that pins it against
  `presence.EncodeValue` over every byte value, U+2028/9 and invalid UTF-8.
- A test that hands a map to the decoder and compares the result against that
  same map cannot fail, because the repair rewrites in place and the decode
  returns the map it was given. Cloning on the way in is what made the
  "legitimate values are left alone" assertion mean anything; verified by
  forcing the repair on and watching it fail.

## Human takeover: the repair must not write to its input

- The agent loop stopped on one blocking finding: `repairPresence` wrote into
  the protobuf message, and `ToPresence`/`ToPresenceChange` put a live
  document's presence map into that message without copying it. An in-process
  encode then decode -- the conformance runner, many document tests -- would
  rewrite the sender's own presence. The repair now returns a copy when it
  changes anything and runs inside `fromPresence`, which also brings the last
  decode site (`FromDocumentSummary`) under it. A decoder that writes to its
  input is only safe if no encoder aliases into that input; check the encoders
  before mutating a message on the way in.
- The growth predictor was exact only for the toolchain it was written on. Go
  1.27 replaces an invalid UTF-8 byte with the raw U+FFFD (three bytes) where
  1.26 wrote the `�` escape (six), so the pinning test failed locally while
  CI on 1.26 stayed green. The count is now an upper bound for invalid bytes,
  and the test asserts exactness only for valid UTF-8.
- A budget drop to `""` is logged, so a server-side rewrite of client data is
  never silent.
