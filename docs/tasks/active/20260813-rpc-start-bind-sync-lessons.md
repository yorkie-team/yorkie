# Lessons: RPC start bind sync

**Created**: 2026-08-13

## A red CI job on a PR is not always the PR's fault

The diff on #1925 touched `server/backend/warehouse` and
`server/projects` only. Neither is reachable from `test/bench`. Checking
that first — before reading any of the changed code — pointed straight
at the harness instead of the change, and `main` being green just meant
the race had not been lost there yet.

## "connection refused" names its own root cause

`ECONNREFUSED` means no socket is bound, not that the server is slow or
the handler is broken. That narrows the search to the listen path
immediately. `Start` spawning a goroutine and returning `nil` was
visible in six lines of `listenAndServe`.

## Grep for the existing workaround

`helper.WaitForServerToStart` existed and was used by exactly the two
test packages that build an `rpc.Server` by hand. A readiness poll that
only some callers use is a sign the thing being polled should be
synchronous. Fixing `Start` removes the need for the workaround rather
than spreading it to more callers.

## A silent failure hides a second one

Making `Start` return the bind error immediately exposed that
`server/packs` and `server/rpc` had been fighting over port 11101, with
packs' tests silently running against the other package's server. Errors
that are only logged do not just hide themselves — they let unrelated
bugs live behind them.

## Replacing a stdlib wrapper means inheriting what it did for you

Swapping `ListenAndServeTLS` for `ServeTLS` looked like a pure "hoist
the listen out" refactor. It was not: `ListenAndServeTLS` carries a
`defer ln.Close()` that `ServeTLS` does not, precisely because
`ServeTLS` can return before `Serve` takes ownership. Read the wrapper
being replaced line by line — the difference between it and the inner
call *is* the thing you now have to do yourself.

## Don't take a timing race's local pass as evidence

The "port accepts connections immediately after Start" assertion passed
on an unloaded laptop *before* the fix; the race window is nanoseconds.
The deterministic RED had to come from the other half of the same root
cause — `Start` returning `nil` on a taken port. When a race won't
reproduce, look for a non-racy symptom of the same defect to test.
