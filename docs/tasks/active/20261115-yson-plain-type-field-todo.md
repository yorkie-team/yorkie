# Restore a revision whose root holds a plain object with a `type` field

**Created**: 2026-11-15

Issue: #2364 — `RestoreRevision` fails with `[invalid_argument] unsupported
element` whenever the document root contains a plain object carrying a string
`type` key, e.g. `{ type: "sheet", name: "Sheet1" }`.

## Problem

`yson.Unmarshal` preprocesses YSON constructors (`Int(42)`, `Counter(Int(1))`,
`Text([…])`, …) into intermediate JSON objects shaped
`{"type":"<ctor>","value":<arg>}` so that `encoding/json` can decode them.
`parseObject`/`parseArray` then treat *any* decoded map carrying a string
`type` key as one of those intermediate objects and hand it to
`parseTypedValue`:

- unknown type -> `ErrUnsupported` ("unsupported element")
- `{type:"Date",value:"x"}` -> `parse date: invalid YSON`
- `{type:"Text",value:"x"}` -> `parse text: invalid YSON`

So the tag preprocessing writes lives in the same namespace as user data.
Objects with a `type` field are ordinary application data (Wafflebase sheet
tabs, slide elements), so every restore of such a document fails.

## Approach

Tag the intermediate objects with a key user data cannot mean: emit
`{"$$yson$$type":"<ctor>","value":<arg>}` instead of `{"type":…}`, and look
the tag up under that reserved key in `parseObject`, `parseArray`,
`parseTypedValue`, `parseCounter` and `Unmarshal`. A plain `type` key is then
always user data, and `parseTreeNode` keeps reading the plain `type` key
because tree-node types are user data too.

To keep the reserved key genuinely unambiguous, reject input that uses it as
an object key (after JSON unescaping) before any rewriting happens.

No stored data changes: `Marshal` always emits typed values as constructors
(`Int(…)`, `Counter(…)`, `Date(…)`, `BinData(…)`, `Text(…)`, `Tree(…)`), never
the intermediate `{"type":…}` form, so existing revision snapshots read back
unchanged.

## Steps

- [x] Reserve a tag key and rewrite preprocessing to emit it
- [x] Read the tag under the reserved key in every parse path
- [x] Reject the reserved key when it appears as an input object key
- [x] Unit tests: plain objects with `type`, with `type`+`value`, nested in
      arrays/objects, round-trip through `Marshal`; reserved-key rejection
- [x] Integration test: restore a revision whose root holds
      `{ id, name, type: "sheet" }`
- [x] `make lint`, `go test ./pkg/document/yson/...`

## Out of scope

- The JS SDK side (the server parses the snapshot; the SDK only sends the
  revision ID).
- Any protobuf/API change.
