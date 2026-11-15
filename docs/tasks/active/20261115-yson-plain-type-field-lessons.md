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
  the same key be spelled many ways (`$$type`…), so the guard unescapes a
  literal before comparing, and only for literals that are object keys (the
  next non-space byte is `:`) — a plain *string value* equal to the reserved
  key is harmless.
- The guard has to run before `rewriteDedupCounters`, otherwise it would trip
  over the reserved key that pass emits itself.

## Review rounds

- Round 1–3: not run. This change was produced by an autonomous headless run
  with no reviewer subagent available (`/self-review` dispatches one). The PR
  is left as a draft for CI, `@claude review` and a human reviewer.
