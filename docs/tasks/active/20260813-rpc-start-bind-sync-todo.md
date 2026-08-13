# RPC: bind the listener before Start returns

**Created**: 2026-08-13

The `bench` job on yorkie-team/yorkie#1925 failed with
`unavailable: dial tcp [::1]:11501: connect: connection refused`, right
after `BenchmarkGetChannels` finished and `BenchmarkGetDocuments` started
its own server. The PR's diff (warehouse/project stats) does not reach
the benchmark path, so the failure is a pre-existing race the loaded CI
runner happened to lose.

## Problem

`rpc.Server.listenAndServe` ran `http.Server.ListenAndServe` inside a
goroutine and returned `nil` immediately:

```go
func (s *Server) listenAndServe() error {
	go func() {
		if err := s.httpServer.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
			logging.DefaultLogger().Errorf("HTTP server ListenAndServe: %v", err)
		}
	}()
	return nil
}
```

`net.Listen` therefore happens after `Start` has already returned. Two
consequences:

1. A client dialing right after `Start` can get `ECONNREFUSED` because
   the socket is not bound yet. `helper.TestServerWithSnapshotCfg` dials
   the admin client immediately after `y.Start()`, and every benchmark
   that builds a server goes through it — hence the flaky `bench` job.
   `server/rpc` and `server/packs` already worked around this with
   `helper.WaitForServerToStart`; the shared test helpers never did.
2. A bind failure is only logged. `Start` reports success while nothing
   is listening, so a port conflict looks like a healthy server.

## Plan

- [x] Reproduce the second consequence deterministically (RED):
      `Start` returns `nil` when the port is already taken
- [x] Open the listener in `listenAndServe` and hand it to
      `Serve`/`ServeTLS`, so `Start` returns only once the port accepts
      connections and a bind error propagates
- [x] Give `server/packs` its own RPC port — see below
- [x] `make lint`, full `-tags integration` suite,
      `BenchmarkGetChannels`/`BenchmarkGetDocuments` back to back

## Fallout: server/packs shared server/rpc's port

Both test packages bound `helper.RPCPort` (11101). Whenever the two ran
concurrently, packs' bind failed silently and
`WaitForServerToStart("localhost:11101")` connected to *server/rpc's*
server — so packs' tests ran against a different backend instance than
the one they set up, including its mock DB. With `Start` now returning
the bind error, that turns into a hard failure, so packs gets
`helper.PacksRPCPort` (11001). `TestConfig` hands out `RPCPort + 100n`,
so ports below `RPCPort` stay free for fixed-port packages.

## Review

- `server/rpc/server.go` — listener opened in `listenAndServe`;
  `ListenAndServe`/`ListenAndServeTLS` → `Serve`/`ServeTLS`
- `server/rpc/server_test.go` — `TestServerStart`: port accepts
  connections immediately after `Start`; `Start` errors on a taken port
- `test/helper/helper.go` — `PacksRPCPort`
- `server/packs/pushpull_test.go` — uses `PacksRPCPort`

## Review round 1 (CodeRabbit)

Both findings landed on this commit and both were valid:

- **`ServeTLS` leaks the listener.** `Serve` closes the listener itself
  (`net/http/server.go` `defer l.Close()`), but `ServeTLS` returns early
  on HTTP/2 setup or `LoadX509KeyPair` failure, before it ever reaches
  `Serve`. `ListenAndServeTLS` — the call this commit replaced — has a
  compensating `defer ln.Close()`, so dropping it was a regression: a
  bad key pair would leave the port bound, so clients would connect and
  hang instead of being refused. Added `defer lis.Close()` to the TLS
  branch, plus a test that a bogus key pair releases the port.
- **`assert` in test setup.** `rpc.NewServer` returns `nil` on error and
  `net.Dial`/`net.Listen` return nil values, so a setup failure panicked
  the test binary instead of failing the subtest — which would have
  destroyed the diagnostic value of the very test meant to catch a
  regression here. Setup calls now use `require`.

## Notes

`server/profiling/server.go` has the same goroutine-wrapped
`ListenAndServe`. It is left alone here: nothing dials the profiling
port right after `Start`, and changing it would turn a currently silent
profiling bind conflict into a startup failure — worth its own change.
