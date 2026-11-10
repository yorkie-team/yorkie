# Lessons: presence base keyed behind the stored checkpoint

## Comparing only client seq and epoch is not enough

A presence changes only on a push, and every push moves the client seq, so the
server seq looked unnecessary for the freshness check. It is not:
`a base from an earlier attachment is not used` relies on it to tell a
re-attachment through another server apart, because the client seq starts
over there and can land on the cached one again. Dropping the server seq from
the check made that test fail.

## Self review, round 1 (correctness and tests)

- Fixed: the stored-client read failed silently. It now logs a warning and
  falls back to the given client, which is the behavior before this change.
- Fixed: the read ran even when `fold` was nil. It is now skipped.
- Fixed: `commit`'s doc comment and the design doc still described keying to
  the reply's checkpoint.
- Fixed: the test did not assert its own premise (the push-only reply carries
  the old server seq) or the folded presence a peer pulls.
- Not taken: record `max(cpBeforePush, reply)` in the in-memory `clientInfo`
  instead of reading the store. It fixes the same case without a read, but it
  repeats the store's `$max` rule in a second place. Reading the stored client
  keeps the presence base on the same source as the client cache.

## Self review, round 2 (design fit)

No correctness findings. Taken:

- The read ran for every push with a fold, including those that only drop the
  base. `commit` now takes a getter and reads only when it keeps a base, after
  the size check. This also puts the "key to the stored client" rule inside
  `commit` instead of leaving it to the caller.
- A failed read fell back to the reply's checkpoint, which is the bug this
  fixes and, with resumable attach seeding a client-sent checkpoint, could
  match a later attachment. It now drops the base.
- Unit tests for keying to the stored checkpoint, dropping on a failed read,
  and not reading for a clear.
- Corrected the round-1 note that the read is always a client-cache hit: it is
  on Mongo right after `UpdateClientInfoAfterPushPull`, unless the entry was
  evicted; the memory database reads its table. The read now happens only when
  a base is kept.

Not taken:

- Return the stored client from `UpdateClientInfoAfterPushPull`, or have it
  fill the given `clientInfo`, so no read is needed. Returning it changes the
  interface for about 80 test call sites; filling the argument is a side
  effect no other database method has. Reading the stored client back keeps
  the presence base on the same source as the client cache without either.

Known limitations: the memory database path and the server-client branch of
`storedClientInfo` are covered only by the existing tests, not by a push-only
scenario.

## Review panel, round 1 (correctness)

- Fixed: `commit` trusted the read-back checkpoint unconditionally. The pull
  lock is per-process, so a concurrent push by the same client on another
  server can advance the stored client seq between the pull and the read-back,
  and this request's older presence would then be cached under the newer
  checkpoint — which the client's next patch would fold onto. `commit` now
  takes the checkpoint this request's own push produced and drops the base
  when the stored client seq no longer matches it.
- The server seq is deliberately not compared. It can move under a concurrent
  pull without the presence going stale, and an entry keyed behind it only
  misses, which costs a full put rather than correctness. Comparing it would
  also undo the push-only case this task exists for.
