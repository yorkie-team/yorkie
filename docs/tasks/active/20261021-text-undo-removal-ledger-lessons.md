**Created**: 2026-10-21

# Lessons: Keep a Text removal the undo never saw

- The reproduction needs the protobuf round trip: an in-process exchange
  keeps `RemovedBy` on the operation objects even when the converter drops
  it.
- Once an undo can revive a tombstone, "the deleter knew the node" is not
  the rule for a remote deletion any more; it has to be "the node was live in
  the deleter's view", or a deletion removes text its author never saw.
- A server does not pass unknown fields through. It decodes the pack and
  encodes it again, so a new field that changes what an operation does needs
  a capability, the same as `presence-patch`.
- Recreating a purged run as one node made the collected replica's node
  partition differ from an uncollected one. Recreate along the purged pieces'
  own boundaries.
