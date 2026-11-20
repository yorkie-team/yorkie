**Created**: 2026-11-19

# Validate a Go-built yson.Counter payload off the text decoder

Issue: #2389 (follow-up from #2385).

## Problem

`crdt.ValidateHLLPayload` is only called from `yson.parseDedupCounter`
(`pkg/document/yson/yson.go:702`), so the guard covers payloads that arrive
as YSON *text* and nothing else. A `yson.Counter` built in Go never passes
the decoder:

- `client.WithInitialRoot` (`client/options.go:213`) stores a `yson.Object`
  that `Client.Attach` applies with `r.SetYSONElement(k, v)`
  (`client/client.go:1155`).
- `json.Object.SetYSONElement` → `setNewCounter(..., y.Registers)`
  (`pkg/document/json/object.go:263`) and `json.Array.AddYSON` →
  `AddNewCounter` (`pkg/document/json/array.go:225`) both `panic(err)` when
  `RestoreHLL` rejects a register slice that is not 16384 bytes.

So an SDK application that builds a dedup counter root from its own
(possibly user-derived) bytes panics inside `doc.Update`, which is exactly
what the guard was meant to prevent.

Related asymmetry: `yson.Counter.Marshal` (`pkg/document/yson/yson.go:267`)
happily encodes a wrong-length register slice that `Unmarshal` now refuses,
so such a value can no longer round-trip — and `admin.Client.CreateDocument`
(`admin/client.go:354`) marshals a caller-built root, which then fails at
the server rather than locally.

## Plan

1. `pkg/document/yson`: add `Counter.Validate()`, `Object.Validate()` and
   `Array.Validate()` — a `Counter` of type `IntegerDedupCnt` with non-nil
   registers must pass `crdt.ValidateHLLPayload`; `Object`/`Array` recurse.
2. `Counter.Marshal`: validate before encoding, so Marshal and Unmarshal
   agree on what is a legal value and the admin path fails locally.
3. `client.Attach`: validate `attachOpts.InitialRoot` before any RPC, so a
   malformed root is an `Attach` error instead of a panic in step 05's
   `d.Update`.
4. Update the `setNewCounter` / `AddNewCounter` doc comments to name both
   guards instead of only the text decoder.
5. Tests: yson unit tests for Validate / Marshal rejection; a client unit
   test that `Attach` with a malformed initial root returns an error.

## Acceptance criteria

- A `yson.Counter` with a wrong-length register slice cannot reach
  `RestoreHLL` through `client.WithInitialRoot`; `Attach` returns an error.
- `Counter.Marshal` rejects what `Unmarshal` rejects (round-trip symmetry).
- Existing YSON text-decoder behavior is unchanged.
- `make lint` and the touched packages' unit tests are green.

## Out of scope

- Turning the `json` setters into error-returning APIs: every setter there
  panics by design, and the panic message from `ValidateHLLPayload` is
  already precise. The fix is to keep untrusted payloads off that path.
