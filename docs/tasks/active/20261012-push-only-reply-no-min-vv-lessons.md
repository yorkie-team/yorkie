**Created**: 2026-10-12

# Lessons: omitting minVersionVector from a push-only reply

## The vector is safe only for a replica that pulled with it

`minVersionVector` is the minimum over all clients' recorded vectors, so
it is a statement about what every replica has, not about what *this*
replica has applied. Collecting with it is sound only for a client that
has just applied every change the server held when the vector was taken
— which is exactly what a pull reply gives and a push-only ack does not.
The same reasoning is why a snapshot reply carries the document's own
vector instead (the server already collected before serializing).

## Check who reads the field before dropping it from the wire

Before omitting the vector, every consumer of a push-only reply had to
be checked for a non-GC use (clock sync being the plausible one). In
this repo: the Go SDK's push-only branch calls
`AcknowledgePushedChanges`, which touches only `Checkpoint.ClientSeq`
and `IsRemoved`, and the three server-side push-only callers
(`documents.UpdateDocument`, `revisions.restore`, the cluster detach
handler) discard the returned pack. The lamport-catch-up concern that
forced the disable-GC snapshot path to keep a one-entry vector does not
apply here: a push-only client is the author of everything it pushed, so
its clock is already ahead of the ack.

## Precedent on the wire beats a new shape

The disable-GC attachment path already returns change pulls with
`VersionVector` nil, so clients and the converter handle a reply without
one. Reusing that shape meant the fix needed no API change and no new
client-side branch.
