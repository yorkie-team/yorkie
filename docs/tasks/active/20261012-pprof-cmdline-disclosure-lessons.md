**Created**: 2026-10-12

# Refuse the pprof cmdline Route on the Profiling Mux — Lessons

Plan: `20261012-pprof-cmdline-disclosure-todo.md`.

## RED evidence

`server/profiling/cmdline_test.go` against the unmodified mux
(`go test -race -run 'TestPprof|TestMetrics' ./server/profiling/`):

```text
--- FAIL: TestPprofCmdlineIsRefused/pprof_enabled
    /debug/pprof/cmdline must not succeed
    /debug/pprof/cmdline disclosed a credential      (x3 sentinels)
    /debug/pprof/cmdline?debug=1 disclosed a credential
    /debug/pprof//cmdline disclosed a credential
    /debug/pprof/heap/../cmdline disclosed a credential
    /debug/pprof/%63mdline disclosed a credential
```

The failing body was the literal `os.Args` join, sentinels and all:

```text
"yorkie\x00server\x00--backend-secret-key\x00SENTINEL-admin-signing-key-ca7b1f\x00..."
```

`pprof_disabled` passed before the fix as well: nothing is registered then,
and the mux 404s. It is kept because it is half of the acceptance criteria —
the guarantee has to hold under both settings, not just the one that was
broken.

## Notes

- `pprof.Index` is a subtree handler. Go's implementation strips the
  `/debug/pprof/` prefix and dispatches the remainder through
  `pprof.Lookup`, which does not know `cmdline`, so today the fallback
  answers `404 Unknown profile` by accident rather than by intent. The
  refusal is written explicitly so a stdlib change cannot quietly re-open
  the route: Go 1.26's index page still advertises a `cmdline` link.
- `http.ServeMux` redirects an unclean path (`//cmdline`, `x/../cmdline`)
  with a 301 instead of dispatching it, so the test follows one redirect
  before asserting. Percent-escapes (`%63mdline`) are unescaped per segment
  by the mux and so match the exact pattern directly.
- The index page keeps listing `cmdline`, because the listing lives inside
  the stdlib handler and reproducing it here would mean vendoring the whole
  page. The link now answers 404.

## Guard coverage

The first version of the suite went through the mux only, which could not
tell the `withoutCmdline` guard apart from the stdlib's accidental 404:
`http.ServeMux` routes every cmdline spelling except a trailing slash onto
the exact `refuseCmdline` pattern, and `pprof.Index` answers
`/debug/pprof/cmdline/` with its own `Unknown profile` anyway. Unwiring the
guard left the suite green. Two tests now pin it:

- `TestCmdlineGuardRefusesBeforeIndex` drives `withoutCmdline` directly with
  a spy for `pprof.Index`, so the normalization in `isCmdlinePath` is what
  is under test rather than the mux's routing.
- `TestCmdlineGuardIsWiredToIndexSubtree` asserts the trailing-slash form is
  routed to the index subtree and that its refusal carries no `X-Go-Pprof`
  header — the marker `pprof.serveError` sets and `refuseCmdline` does not.

Both fail against a mux built without the guard, and the second also fails
if `isCmdlinePath` stops cleaning the path.

## CI note: the integration failure is not this branch's

The `test` lane on `66d4ce64` failed in
`TestAuthWebhookPresenceOnly/a reader attaching alone keeps the bound schema`
(`test/integration/auth_webhook_test.go`, which arrived with #2129). Nothing
on this branch reaches it:

- The only code this branch changes is the profiling mux; `cmd/yorkie` and
  `server/server.go` take comment edits alone.
- `server/profiling`'s tests plant sentinels in `os.Args`, but a Go test
  binary is per package, so that mutation cannot be observed from the
  `test/integration` process.
- The same lane passed on the two preceding commits of this branch
  (`291d0c57`, `c02f7c52`). The delta from the last green run to the red one
  is `server/profiling/cmdline_test.go` and this file.

The subtest turns an attachment limit on so that each attach reaches the
schema rebind, which makes it sensitive to when a detach is reflected in the
attachment count — the shape of a timing flake rather than of a verdict on
this change. Diagnosing it means touching an integration test this branch
does not own, so it is left for a maintainer.

## Self review

Not run: this branch was produced by an autonomous one-shot run with no
reviewer subagent available. CI, `@claude review` and a human review on the
PR stand in for it. Recording it here rather than letting a skipped round
read as a clean one.
