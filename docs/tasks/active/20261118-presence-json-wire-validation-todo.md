# Enforce the presence JSON wire contract on the server

**Created**: 2026-11-18

Issue: #2284 (follow-up filed from the review of #2280).

## Problem

`docs/design/presence.md` declares that every presence value on the wire is the
JSON encoding of the value, and that a value which is not valid JSON "makes a JS
peer fail to apply the whole pack that carries it". That contract is enforced
only on the Go SDK's write side (`presence.Presence.Set`). The server — the
trust boundary — enforces nothing: `converter.FromPresenceChange` hands
`pbPresence.GetData()` through verbatim, `server/packs` stores it in the change
history and the next snapshot, and fans it out. A client speaking the
gRPC/Connect API directly (or any Go app calling `SetJSON` with non-JSON text)
can therefore persist `{"cursor": "moved"}` and wedge every JS peer on that
document, repeatedly, with nothing more than presence-write access.

## Plan

1. **Strict at the wire boundary.** `FromPresenceChange` rejects a `PUT`/`PATCH`
   carrying a value that is not valid JSON, with a new
   `ErrInvalidPresenceValue` (`errors.InvalidArgument`, so the RPC layer maps it
   to `InvalidArgument` rather than `Internal`).

2. **Lenient on the stored path.** `FromPresenceChange` is also the read path
   for rows persisted under the looser rules
   (`database.PresenceChangeFromBytes`), and rejecting there would make the
   document holding such a row permanently unreadable — the exact poison pill
   `converter.FromStoredOperations` exists to avoid. Add the presence twin,
   `FromStoredPresenceChange`, which repairs before it decodes: a stored value
   that is not valid JSON is JSON-encoded, which is what it already meant
   (a bare string), so Go readers see the same text and JS readers can finally
   parse it.

3. **Snapshot decode too.** `fromPresences` (snapshot read path) normalizes the
   same way, so a document rebuilt from a legacy snapshot no longer re-emits the
   poisoned value into the next snapshot.

4. Tests in `api/converter` for both sides, plus a `server/packs` or
   `server/rpc` level test that the ingress path refuses the poisoned pack.

5. Update `docs/design/presence.md` (Presence Value Encoding) and the `SetJSON`
   doc comment, which currently promises the value is stored as given.

## Out of scope

- Decoding presence at the admin API boundary.
- Any protobuf/API change (this run cannot regenerate `api/yorkie/v1`).
