**Created**: 2026-11-10

# Channel Batch Window

Design: [channel-batch-window](../../design/channel-batch-window.md)

Channel events wait for a fixed 100ms ticker (p95 ~100ms one way on a
local server). Deliver them on a leading-edge throttle and make the window
a server default with a per-project override.

## Plan

- [x] Red: `BatchPublisher` tests that fail on the ticker — an event on an
      idle throttled publisher arrives well within the window; a burst is
      delivered at most once per window; `SetWindow` takes effect.
- [x] `BatchPublisher` throttle mode (one-slot signal, timer armed only
      while events wait), `SetWindow`; channel publisher uses it; document
      publisher unchanged.
- [x] Server config `ChannelBatchWindow` + `--channel-batch-window`
      (default 100ms, `[0s, 1s]`).
- [x] Project field: proto (`make proto`), `api/types`, validator,
      `project_info.go`, converters, CLI `update` / `list`, testcases.
- [x] Resolve the window in `yorkieServer.subscribeChannel`, pass it to
      `PubSub.SubscribeChannel`; set it on an existing publisher.
- [x] Integration test: a project with a small window delivers a
      broadcast faster than the default; an update reaches the next
      subscriber.
- [x] `docs/design/pub-sub.md`: note the channel throttle.
- [x] `make verify`, `make test` (MongoDB), self-review (max 3 rounds).
- [x] Measure: broadcast latency before/after with the default and a
      small window.

## Review

- `make verify` green; `make test` green (integration 177s) before the
  review fixes, channel integration tests green after them; new unit tests
  green under `-race`.
- Mutants caught: channel back on the ticker (unit), no `SetWindow`, project
  value ignored, fallback passing 0 (integration).
- Self-review: round 1 (correctness/tests) and round 2 (design fit), no
  blocking findings; fixes logged in the lessons file.
- Broadcast latency, 15 Hz sender and one watcher, local server (p50 / p95):
  main 56 / 100 ms; this branch at the 100 ms default 57 / 103 ms (a stream
  faster than the window is batched once per window either way); 10 ms
  window 5 / 13 ms; 0s window 6 / 13 ms.
- Follow-up: dashboard form for the project field.
