**Created**: 2026-10-08

# Lessons — Watch stream heartbeat and SDK idle timeout

## Notes

- `handleWatchResponse` in `client/client.go` ends with
  `return nil, ErrUnsupportedWatchResponseType`, and the reader goroutine
  treats that error as terminal: it pushes the error, closes the buffer and
  does not reconnect. So a new oneof case in `WatchResponse` is **not**
  transparent to this repo's Go client the way the issue says it is to old JS
  SDKs. `pumpChannelWatch`, by contrast, switches without a default and
  ignores unknown cases for free.
- The heartbeat ticker belongs in `streamMergedEvents` rather than around
  `stream.Send`: that function already owns the single select loop every
  response goes through, so one `case <-ticker.C` covers the whole stream and
  resetting the ticker after each event keeps a busy stream heartbeat-free.
- An idle timeout on the client is cheapest as a `context.CancelFunc` armed by
  a `time.AfterFunc` and reset on every received response: cancelling the
  watch context surfaces as a stream error, and the reader loop's existing
  reconnect path then does the recovery. No new reconnect logic.

- The advertised heartbeat interval is attacker-controlled input in the same
  sense any wire field is: it decides a timer the client arms against itself.
  Clamping it on both ends (`watchIdleTimeoutMin`/`Max` in `client/client.go`)
  is what keeps a one-millisecond advertisement from turning the reconnect
  path into a loop that opens streams as fast as the transport allows. The
  upper bound alone only stops the overflow, not the spin.
- The initialization response's heartbeat field was unreachable from a unit
  test while it was built inline in `Watch`, which needs a backend. Pulling it
  into `sendWatchInitialization` makes the advertised value and its unit
  assertable without a database.

## Self review

`/self-review` was not run: this was an autonomous one-shot run with no tool
able to launch a reviewer subagent. The branch's reviewers are CI,
`@claude review` and a human on the draft PR. Recording the gap so a skipped
round is not read as a clean one.
