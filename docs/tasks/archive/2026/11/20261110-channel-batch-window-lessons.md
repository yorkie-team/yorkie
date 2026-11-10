# Lessons: Channel Batch Window

## Findings

- The channel `BatchPublisher` saves no messages: it has no `OnEnqueue`
  and `publish()` sends each event to each subscriber separately, so its
  100ms ticker is pure latency (local measurement: p95 100ms → 16ms with
  a 5ms window).
- Dead subscriptions only die inside `publish()` and are reaped by the
  same call, so dropping the idle ticker does not strand them.

- Red came first: the idle-channel test failed on the ticker (event held
  until the next tick) and passed on the throttle; flipping the channel
  config back to `Throttle: false` fails it again.
- Timing two broadcasts sent back to back is racy: the second can join the
  first's batch before the publisher wakes, so their gap is 0 whatever the
  window. The integration test first let a mutant (no `SetWindow`) pass.
  Send the second only after the first arrived; then only the window can
  hold it back, and both mutants (no `SetWindow`, project value ignored)
  fail.
- `make verify`'s goconst counts string literals across the test helper
  file: a new `"10ms"` made three and failed lint.
- Local MongoDB: the container was healthy but the host port timed out
  (server selection error). Restarting that one container fixed it; no
  Docker restart needed.

## Self-review log

### Round 1 — correctness/tests (no blocking findings)

Concurrency checked clean (no lost wake-up: append under the mutex, then a
non-blocking send to a 1-slot channel; worst case an extra empty publish);
30× `-race` runs stable. Non-blocking findings and what was done:

1. A project could not go back to the server default (validator rejected
   `""`). Fixed: `""` clears it. The CLI's duration flag still cannot send
   `""`; the admin API and dashboard can.
2. `SetWindow` comment said the next delivery follows the new window, but
   an armed timer keeps the old one. Fixed the comment and the design doc
   (lag ≤ one old window).
3. A change reaches an open channel only with its next subscriber. Already
   in the design doc; added to the CLI help.
4. Test gaps. Added: the RPC fallback to the server default (integration),
   `Close` with an armed timer, an event right after the timer's publish.
   The first draft of the last one was wrong: under synctest the test's
   `Sleep` and the timer wake at the same instant, so the event joined the
   timer's batch. Wait for the timer's publish before sending.
5. `ParseChannelBatchWindow` relies on `Validate` for the range, like its
   siblings. Kept.

### Round 2 — design fit (no blocking findings; loop stops here)

1. The default sat in `database` though no row stores it (projects keep
   `""`). Moved to `types.DefaultChannelBatchWindow` beside the bound.
2. User-facing spots of "empty = server default": the CLI flag printed a
   misleading `(default 100ms)` (now 0, sent only when passed), `list`
   showed a blank (now `default`), the validator message hardcoded `1s`
   (now built from the bound).
3. The RPC path re-parsed the flag (with an `os.Exit` fallback) on every
   subscribe. Parsed once into `Backend.ChannelBatchWindow`, like the
   precedent passing `ParseChannelSessionTTL()` to the channel manager.
4. Tick mode allocated the throttle's wake-up channel; `SetWindow`'s comment
   named a channel-only bound in a generic type. Both fixed.
5. Cluster mode was right but undocumented: added a paragraph (per-node
   resolution, cache invalidation, `ProjectCacheTTL` worst case).
6. The dashboard form is a follow-up (yorkie-team/dashboard), as the
   precedent did with dashboard#298; it must be able to clear to `""`.

