**Created**: 2026-10-05

# `project update` CLI: stale `ALL`, resent untouched fields (#2106)

## Problem

1. `--auth-webhook-method-add ALL` expands a hard-coded list of 10 methods;
   `types.AuthMethods()` has 18 (revision and channel methods missing).
2. `project update` resends every field with its current value. A project
   stored with `ChannelSessionTTL: ""` (server default at runtime) is
   rejected by the `channel_session_ttl` validator on any update, even one
   that only touches `--auth-webhook-url`.
3. Found while reading: fields guarded by `flag != 0` whose flag default is
   non-zero (`--auth-webhook-{min,max}-wait-interval`,
   `--auth-webhook-request-timeout`, the event-webhook trio,
   `--client-deactivate-threshold`) are always sent with the flag default,
   so any update silently resets them. `--max-*-per-document 0` (no limit)
   could not be set at all.

## Plan

- [x] `types.Method.IsDeprecated()` for the `WatchDocument`/`WatchChannel`
      aliases; unit test.
- [x] Derive the CLI `ALL` list from `types.AuthMethods()` minus deprecated
      aliases (`Project.RequireAuth` already matches the aliases via
      `Watch`).
- [x] Event-webhook `ALL`: checked, not stale (one event type, matches
      `IsValidEventType`). Left as is.
- [x] Build `UpdatableProjectFields` from `Changed` flags only; untouched
      fields stay nil (server `UpdateFields` and the Mongo `$set` skip nil).
- [x] No flag at all: fail in the CLI before dialing, instead of a round trip
      ending in `ErrEmptyProjectFields`.
- [x] Sort the resulting method/event slices for stable output.
- [x] Unit tests for field building and `ALL` expansion.
- [x] End-to-end: built CLI against a throwaway memory-DB server (scratch
      HOME): no-flag error, `ALL` → 16 methods, non-default timings survive
      later updates, `--max-subscribers-per-document 0`, `rm ALL`. No CLI
      integration harness exists, so the empty-TTL case is covered by the
      unit test (fields omit it and `Validate()` passes).
- [x] `make verify`. `make test` not run: the change does not reach the
      integration lane (no test there drives the CLI; `api/types` gains only
      an additive method).
- [x] One self-review pass.

## Non-goals

- Relaxing the `channel_session_ttl` validator to accept `""` (see lessons).
- Dashboard update path — tracked separately.
