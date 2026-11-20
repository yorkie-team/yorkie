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

1. **Repair at the wire boundary, do not reject.** `FromPresenceChange`
   JSON-encodes, in place, a `PUT`/`PATCH` value no peer can parse
   (`repairPresence`), so what the server stores and fans out is always
   parseable. Rejection is not available: the same function is the server's
   ingress, the Go client's pull decoder, and the read path for rows and
   snapshots persisted under the looser rules. Refusing at ingress would reject
   the whole change pack of every client on a Go SDK older than
   `presence.EncodeValue` (0.6.36); refusing on pull would wedge the client;
   refusing on a stored read would make the document permanently unreadable —
   the exact poison pill `converter.FromStoredOperations` exists to avoid. One
   repair covers all three, and needs no rollout ordering.

2. **Bound the nesting as well as the syntax.** "Valid JSON" is weaker than
   "a peer can parse it": Go's scanner accepts 10000 levels of nesting, V8's
   `JSON.parse` overflows its stack first. A value deeper than
   `maxPresenceValueDepth` is repaired the same way as unparseable text.

3. **Snapshot decode too.** `fromPresences` (snapshot read path) repairs the
   same way, so a document rebuilt from a legacy snapshot no longer re-emits
   the poisoned value into the next snapshot.

4. Tests in `api/converter` for the wire path, the ingress pack, the nesting
   bound and the snapshot path.

5. Update `docs/design/presence.md` (Presence Value Encoding) and the `SetJSON`
   doc comment, which currently promises the value is stored as given.

## Out of scope

- Decoding presence at the admin API boundary.
- Any protobuf/API change (this run cannot regenerate `api/yorkie/v1`).
