# Reject malformed HLL register payloads when YSON is parsed

**Created**: 2026-11-18

## Problem

A dedup counter carries its HyperLogLog registers through YSON as a base64
string. `parseDedupCounter` (`pkg/document/yson/yson.go`) decodes that string
and hands the bytes through unchecked. The apply path then calls
`Counter.RestoreHLL` inside `json.Object.setNewCounter`
(`pkg/document/json/object.go:126`) and `json.Array.AddNewCounter`
(`pkg/document/json/array.go:177`), both of which `panic` on a restore error.

`HLL.Restore` errors on exactly one condition: a payload whose length is not
`hllRegisterCount` (16384). So a YSON body holding a dedup counter with a
short or long `"hll"` string panics the goroutine serving the request.

The payload reaches that path from untrusted input:

- admin `CreateDocument` / `UpdateDocument` bodies — `yson.Unmarshal` at
  `server/rpc/admin_server.go:468` and `:655`
- `RestoreRevision` — `server/revisions/revisions.go:193`

Nothing in `server/` recovers a panic, so the blast radius is bounded only by
net/http's per-handler recovery, and the aborted `doc.Update` leaves the
document's clone dirty.

Out-of-range register *values* are no longer part of this: `HLL.Restore`
clamps them to `hllMaxRegisterValue` rather than erroring, so length is the
only remaining failure.

## Goal

Malformed register payloads are rejected where they are parsed, with an
error, so no untrusted input can reach the panicking apply path. Behaviour
for well-formed payloads — including the empty `""` that a counter with no
registers marshals to — is unchanged.

## Plan

- [x] `crdt`: factor the length check into an exported
      `ValidateHLLPayload`, used by `HLL.Restore` so the two cannot drift
- [x] `yson.parseDedupCounter`: validate the decoded payload and return
      `ErrInvalidYSON` instead of handing it to the apply path
- [x] Note the invariant at the two `RestoreHLL` call sites in `json`
- [x] Tests: malformed payload rejected at `Unmarshal` — at the counter,
      object and array positions, so no YSON body carries one into the
      apply path; empty and 16384-byte payloads still round-trip
- [x] `docs/design/counter-dedup.md`: record the parse-time validation
- [x] `make lint`, targeted `go test`

## Non-goals

- Removing the `panic` convention from `pkg/document/json`. Every setter in
  that package panics on a constructor error; the setters return the element,
  not an error, so changing that is an API change well outside this issue.
- Mapping YSON parse errors to `CodeInvalidArgument`. Every other malformed
  YSON body already surfaces the same way; changing it here would single out
  one field.
