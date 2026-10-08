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
