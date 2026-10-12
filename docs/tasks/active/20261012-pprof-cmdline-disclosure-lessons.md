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

## Self review

Not run: this branch was produced by an autonomous one-shot run with no
reviewer subagent available. CI, `@claude review` and a human review on the
PR stand in for it. Recording it here rather than letting a skipped round
read as a clean one.
