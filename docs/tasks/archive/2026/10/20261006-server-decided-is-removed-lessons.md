**Created**: 2026-10-06

# Lessons: Decide Document Removal by Method

## Findings

- `converter.FromChangePack` is shared by the server and the Go client. The
  client reads `IsRemoved` from response packs to learn that a peer removed
  the document, so the converter must keep copying the flag; the server-side
  rule belongs in the handlers, after decoding and before
  `auth.AccessAttributes`, which reads the flag for the verb.
- Setting the flag in one place (`fromChangePack(pb, removes)`) closes both
  issues at once: #2134 is the verb, #2140 is the write in `pushPack`, and
  both read the same field.
- `RemoveOnDetach` sets `IsRemoved` after the webhook, from the server's own
  decision, so overriding the client's flag before the webhook leaves it
  untouched. The cluster `DetachDocument` builds its own pack and never
  decodes one, so it needs nothing.
- Both SDKs send the flag only on `RemoveDocument` (Go `client.Remove`, JS
  `Client.remove`); every other pack comes from `createChangePack`, which
  sets it false. Overriding instead of rejecting keeps an unknown client that
  sets it elsewhere working.
- The new tests fail on all five cases with the handler change reverted
  (removed documents read `{}`, the reader's flagless remove is asked as `r`
  and allowed, the writer's flagless remove leaves the document).

## Self Review

### Round 1 (correctness, compatibility, tests)

Reviewed the full branch diff myself; no separate reviewer was launched.

- Correctness: every pack handler goes through `fromChangePack`; no other
  server path decodes a client pack (`grep FromChangePack server/`). A
  flagless `RemoveDocument` now marks the document removed and publishes the
  removal event, matching what the method already did to the client's status.
- Compatibility: the only visible change for a well-behaved SDK is none; a
  client that sends a flagless `RemoveDocument` is now asked as `rw` and
  actually removes. Webhook decision cache keys change only for that request.
- Tests: cover attach, push-pull and detach with the flag, and remove without
  it as reader (rejected, asked as `rw`) and writer (removes).

No blocking findings. Non-blocking: iOS and Android SDKs were not checked;
override keeps them working either way. Listed in the PR body.
