# Lessons — YSON plain object with a `type` field (#2364)

**Created**: 2026-11-15

## Notes

- The intermediate `{"type":…,"value":…}` shape is produced only by
  `preprocessTypeValues`; nothing outside `yson.Unmarshal` consumes it
  (`grep` for `preprocessTypeValues`/`parseTypedValue` stays inside
  `pkg/document/yson/yson.go`). Every `Marshal` path emits constructors
  instead, so moving the tag to a reserved key needs no migration of stored
  revision snapshots.
- Tree nodes genuinely carry a user-meaningful `type` key
  (`{"type":"p","children":[…]}`), and they are parsed by `parseTreeNode`,
  not `parseTypedValue`. Only the marker lookups move to the reserved key.
- A reserved key is only unambiguous if input cannot also carry it. JSON lets
  the same key be spelled many ways (`$$type`…), so the pass unescapes a
  literal before comparing, and only for literals that are object keys (the
  next non-space byte is `:`) — a plain *string value* equal to the reserved
  key is harmless.
- Reserving a key on the *reading* side only is not enough. `Marshal` writes
  member and attribute keys verbatim, and `json.Object` puts no restriction on
  member names, so a key that `Unmarshal` refuses is still a key a client can
  write — the snapshot produced from it would be permanently unrestorable.
  Escaping on the way in and stripping on the way out keeps the namespace
  private without denying it to callers. The escape marker lives inside the
  reserved namespace itself (`$$yson$$~`), which makes escaping injective: a
  key already spelling the marker escapes to a longer one and comes back
  intact.
- The escape pass has to run before `rewriteDedupCounters`, otherwise it would
  escape the reserved key that pass emits itself.
- A bare constructor (`Int(1)`) unmarshalled into an `Object` would otherwise
  store the tag key as an ordinary member, so `parseObject` rejects a map
  carrying the tag. By then a caller's key of that spelling has already been
  escaped, so the tag can only have come from the rewrite.

## Review rounds

- Round 1–3: not run. This change was produced by an autonomous headless run
  with no reviewer subagent available (`/self-review` dispatches one). The PR
  is left as a draft for CI, `@claude review` and a human reviewer.
