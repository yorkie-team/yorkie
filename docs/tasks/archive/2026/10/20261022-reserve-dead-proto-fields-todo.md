**Created**: 2026-10-22

# Deprecate dead proto fields and correct the MovedAt deprecation plan

Issue: #2276

## Problem

A proto/convergence audit of `api/yorkie/v1/resources.proto` found seven field
slots that neither the Go converter (`api/converter/`) nor the JS SDK
(`packages/sdk/src/api/converter.ts`) writes or reads. They stay declared, so
readers keep assuming they mean something and the generated code keeps them
alive.

Separately, `docs/design/array-move-convergence.md` carries a cleanup plan for
`Element.MovedAt` whose step 2 ("stop calling `SetMovedAt` in `ElementRHT.Set`,
no reader exists") is no longer true: `crdt.PositionedAt`
(`pkg/document/crdt/element_rht.go:171`) reads `MovedAt()` as the LWW tie-break
anchor for an Object key, and snapshot rebuild in `api/converter/from_bytes.go`
depends on it. Following step 2 would make an undo-restored key resolve
differently on different replicas.

## Scope

Dead field slots to mark `[deprecated = true]`. `reserved` was the first
attempt and `buf breaking` rejected it: `api/buf.yaml` sets
`breaking.use: [FILE]`, and that category's `FIELD_NO_DELETE` fires on a
removed number regardless of whether it was reserved — only the `WIRE` and
`WIRE_JSON` categories use the `_UNLESS_NUMBER_RESERVED` variants. Keeping the
numbers declared and deprecating them is the strongest statement available
without relaxing that gate.

| Message | Field |
|---|---|
| `ChangePack` | `min_synced_ticket` (5) |
| `Operation.Edit` | `created_at_map_by_actor` (4) |
| `Operation.Style` | `created_at_map_by_actor` (6) |
| `Operation.TreeEdit` | `created_at_map_by_actor` (4) |
| `Operation.TreeStyle` | `created_at_map_by_actor` (7) |
| `RGANode` | `next` (1) |
| `JSONElementSimple` | `moved_at` (2), `removed_at` (3) |

Not in scope: `JSONElement`'s own `moved_at`/`removed_at` (nested in
`JSONObject`/`JSONArray`/...), which `to_bytes.go` writes and `from_bytes.go`
reads. Only the *simple* element carried by an operation has neither.

## Steps

- [x] Confirm no Go producer or consumer for each slot, excluding
      `api/yorkie/v1/*.pb.go` itself.
- [x] Mark the slots `[deprecated = true]` in `api/yorkie/v1/resources.proto`.
- [x] Regenerate with `make proto` (buf 1.28.1 + the pinned plugins, installed
      via `make tools`; a clean-tree `make proto` was verified to produce no
      diff first, so the regeneration is attributable to this change alone).
- [x] Retarget the one test that used a now-deprecated field as a fixture
      (`api/converter/reissue_test.go`) at fields the walk actually meets, so
      no test depends on a field the schema tells callers not to use.
      `created_at_map_by_actor` covered two branches at once, so it takes two
      fixtures to replace: `VersionVector.vector` for the rekey, and
      `TreeNode.attributes` (`map<string, NodeAttr>`, carried by a real
      `Operation.TreeEdit`) for the descent into message-valued map values.
- [x] Correct the `Element.MovedAt deprecation` section of
      `docs/design/array-move-convergence.md`.
- [x] `make lint` and targeted `go test ./api/... ./pkg/document/...`.

## Notes

- Deprecating a field changes no bytes at all: the slot keeps its number, type
  and name, so every peer decodes exactly as before. The marker is for readers
  and for generated-code callers, which now see a `// Deprecated:` comment.
- Actually reserving these numbers would need `api/buf.yaml` to move from
  `FILE` to `WIRE_JSON` (or to list `FIELD_NO_DELETE` and `MESSAGE_NO_DELETE`
  under `breaking.except`). That is a deliberate loosening of the repository's
  compatibility gate and belongs in its own change, not bundled here.
- `packages/sdk/src/api/yorkie/v1/resources.proto` in yorkie-js-sdk mirrors
  this file and needs the same markers. That is a separate repository and out
  of reach of this change.
- No architecture change, so no new `docs/design/` topic — only a correction to
  an existing one.
