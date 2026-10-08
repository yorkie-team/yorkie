**Created**: 2026-10-05

# Lessons: `project update` CLI fields (#2106)

## Decisions

- **Fix the client, not the validator.** The empty `ChannelSessionTTL` was
  rejected because the CLI resent a field the user never touched. Sending
  only passed options removes the whole class (it also stopped six options
  with non-zero flag defaults from being reset on every update). Relaxing the
  `channel_session_ttl` validator to accept `""` was considered and left out:
  it would add a way to clear a stored TTL back to "server default" through
  the update API, which no caller asks for, and whether `""` survives the
  Mongo `$set` depends on BSON `omitempty` handling of pointers to empty
  strings. If the dashboard turns out to resend the field the same way, fix
  it there the same way.
- **`ALL` excludes deprecated aliases.** `WatchDocument`/`WatchChannel` are
  matched by `Project.RequireAuth` whenever `Watch` is enabled, so adding
  them is noise. `Method.IsDeprecated()` names them once; a test pins that
  every deprecated method is covered by `Watch`, so a new alias cannot be
  marked deprecated without an alias rule.
- **Event-webhook `ALL` left alone.** One event type exists and the list
  matches `IsValidEventType`; not the same bug.
- **No options → CLI error before dialing.** The server would answer
  `ErrEmptyProjectFields` after a login and `GetProject` round trip; failing
  early says what is wrong in CLI terms.
- **`pflag` became a direct dependency** (it was indirect through cobra) for
  the `VisitAll` callback type in `hasUpdateFlag`.

## Self review

One round, weighted to correctness and tests, done in place by the
implementing agent (no separate reviewer could be launched from it).

- No blocking findings.
- Behavior change, non-blocking: `--name ""` used to be ignored; it is now
  sent and rejected by the server's name validation. Passing an empty name
  explicitly is an error either way.
- Behavior change, non-blocking: `--max-{subscribers,attachments}-per-document 0`
  is now sent (before, 0 was indistinguishable from "not passed"), so a limit
  can be lifted from the CLI. BSON `omitempty` keeps non-nil pointers to zero,
  as `RemoveOnDetach: false` already relies on.
- Non-blocking: the no-option error prints cobra's usage, like the
  command's existing "name is required" error.
- `make test` not run: no integration test drives the CLI. Covered by unit
  tests plus a scripted run of the built CLI against a memory-DB server.
