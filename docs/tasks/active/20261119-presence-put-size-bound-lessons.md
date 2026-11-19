# Lessons: Bound the presence a client may push as a put

## Notes

- `converter.FromPresenceChange`, where the finding pointed, is the decode
  path for stored rows as well as for the wire
  (`server/backend/database/change_info.go` calls it when reading a
  `ChangeInfo` back). A size refusal there would turn a row stored before the
  bound into a read error on every pull, so the gate belongs on the push path
  instead, where it can measure only what is about to be stored.
- The bound cannot be `maxPresenceBaseSize`. A presence over 4 KiB is already
  a supported case: it is simply never cached nor folded into, and the client
  pushes full puts. Reusing that constant as a hard refusal would reject
  clients that work today, including the ~7.4 KB presence that motivated the
  patch work in the first place.
- Only the changes above the client's checkpoint are measured, which is the
  set `pushPack` stores. Measuring the whole pack would refuse a reconnecting
  client that resends an already-acked change whose presence the server
  accepted before this bound existed.

## Self review

Not run: this branch was produced by the autonomous agent workflow, which is
granted no tool that can launch the reviewer subagent. CI, `@claude review`
and a human reviewer stand in for it on the PR.
