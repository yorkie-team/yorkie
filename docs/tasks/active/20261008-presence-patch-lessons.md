# Lessons: presence patch (#2154)

- `ChangePack.capabilities` and `api.ServerCapabilities` already existed but
  no server path emitted them. Check for a negotiation channel before adding
  a new response field: the issue suggested the attach response, but the pack
  is returned on push-pull too.
- `ServerCapabilities` listed `element-restore` while the converter does not
  decode `RestoreMode` on element operations. A list that is declared but
  never emitted can drift from what the server honours without any test
  noticing; emitting it is the moment to re-check every entry.
- A locally installed `protoc-gen-connect-go` older than the Makefile pin
  rewrites every connect file. Install the pinned versions into a scratch
  `GOBIN` and put it first on `PATH` for `make proto`.
- A cached base is only safe with a freshness check, and client seq is not
  one: detach resets it and a re-attach can bring it back to the cached
  value. Server seq only grows, so the full checkpoint plus the epoch is.
- An old server does not reject an unknown enum value: the converter's
  switch has no default case and decodes it as an empty change. Check what
  the old code actually does before writing "the old server rejects it".
- Stored rows decode through the same converter as pushed packs, so a strict
  default case must still accept every value the enum defines.
- The checkpoint is not a monotonic identity after all: `DetachDocument`
  zeroes it to exactly the (0, 0) a fresh attach seeds, so on one server a
  base cached past a detach matches the next attachment. "Server seq only
  grows" holds for the document, not for the client's record of it. The fix
  is state the checkpoint does not carry: drop the entry when the client is
  no longer attached, and never trust one at the initial checkpoint.
- Presence sits in no document size gate, so an entry-counted LRU of
  presences is bounded in entries and unbounded in bytes. A cache of
  client-controlled values needs a per-entry byte bound as well as a count;
  here it doubles as the bound on how large a put a small patch can expand
  into.
- A byte bound over a map that counts only key and value bytes is not a
  memory bound: a map entry costs tens of bytes whatever its contents, so a
  presence of one-byte keys weighs orders of magnitude more than the bound
  says. Counting a fixed overhead per entry bounds the key count too, which
  nothing else did.
- Bounding only what is cached leaves the hot path unbounded: the entry
  written back was checked, but the put the fold had already handed the
  store and the fan-out was not. A bound meant to cap amplification has to
  be enforced where the amplification happens.
