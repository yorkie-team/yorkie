# Lessons — Watch heartbeat opt-in via WatchRequest

## Toolchain

The run began without `buf` or the protoc plugins. Rather than hand-edit
generated code, the pinned versions from the Makefile's `tools` target were
installed and `make proto` was run **first with no proto change**: the tree
came back clean, proving the pinned toolchain reproduces the committed
generated code byte for byte. Only then was the field added, so the diff
under `api/yorkie/v1/` is attributable to the proto edit alone. Worth
repeating whenever generated code has to move in an unfamiliar environment —
it converts "I think this is right" into a checked fact.

## Design notes

- A single `heartbeat_interval_ms` request field serves as both the opt-in
  and the tuning knob; no separate `bool` is needed, and it mirrors how
  `actor_id = 3` was added without breaking old SDKs.
- The kill switch lives on the **maximum** bound rather than a third flag:
  "the longest heartbeat interval this server will serve is zero" already
  reads as "no heartbeats", and it keeps the config to two knobs.
- The deprecated `--backend-watch-heartbeat-interval` is honored for its
  on/off intent only (an explicit `0s` still disables heartbeats). Its old
  per-stream meaning cannot survive the move to per-stream opt-in, since the
  whole point is that the server no longer picks one interval for everyone.

## Review rounds

`/self-review` was not run: this autonomous run is granted no tool that can
dispatch the reviewer subagent, and the command's own rules forbid
substituting a self-read of the diff. Recorded here so a skipped round is
not mistaken for a clean one — review falls to CI, `@claude review` and a
human.
