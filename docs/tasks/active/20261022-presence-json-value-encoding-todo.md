**Created**: 2026-10-22

# Go presence values are not JSON-encoded, so JS peers fail to decode them

## Problem

yorkie#2275. `Presence.Data` is `map<string, string>` on the wire, and the JS
SDK treats every value as JSON: `JSON.stringify` on encode, `JSON.parse` on
decode (`packages/sdk/src/api/converter.ts:1139-1145`). Go's
`presence.Presence.Set` stored the string exactly as given, so a Go client
running `p.Set("cursor", "moved")` put the bare word `moved` on the wire.
`JSON.parse("moved")` throws, and the whole pull fails on every JS peer.

Nothing stated the contract: neither the proto comment nor the Go API said
values must be JSON. The reverse direction was wrong too — a JS peer's
`set({name: "peer"})` reached Go as `"peer"`, quotes included.

## Options explored

| Option | Verdict |
|---|---|
| Encode on write at the Go SDK boundary, decode on read | Chosen: both SDKs put the same bytes on the wire, and Go-visible values are unchanged for Go-only deployments |
| Validate in `Set` and panic on non-JSON | Breaks every existing Go app that sets plain strings, to protect a peer it may not have |
| Encode/decode in `api/converter` | The same presence map also reaches MongoDB and snapshots through other paths; the stored form would differ per path |
| Leave the wire alone, document the contract only | The reported pull failure stays |

## Plan

- [x] `presence.EncodeValue`/`DecodeValue`: `JSON.stringify`/`JSON.parse`
      semantics for a single value, HTML escaping off to match JS bytes
- [x] `Presence.Set` and `Presence.Initialize` encode; `Presence.SetJSON`
      stores an already-encoded value (objects, numbers, and the undo path,
      which restores values that were encoded when they were recorded)
- [x] `Document`'s presence accessors and the presence events decode, so a
      Go caller still reads back the string it set
- [x] `InternalDocument` accessors stay raw — the server builds snapshots and
      push-pull payloads from them, and those carry the wire form
- [x] Unit tests: round trip, JS-form input, legacy non-JSON input, undo
- [x] `docs/design/presence.md`: state the value-encoding contract
- [x] `make lint`, `go test ./pkg/... ./client/... ./server/...`

## Review

- Backward compatible on read: a value that is not valid JSON (anything a
  Go client wrote before this change, still sitting in a snapshot) decodes
  to itself.
- Not backward compatible on write for a Go app that was already
  hand-encoding JSON into `Set`: its value is now encoded twice. `SetJSON`
  is the replacement, and the design doc says so.
- Known limitation: a JS peer's non-string value (object, number) decodes to
  its JSON text in Go, because `presence.Data` is `map[string]string` and has
  nowhere else to put it.
