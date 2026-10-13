**Created**: 2026-10-12

# Refuse the pprof cmdline Route on the Profiling Mux

Issue: [#2083](https://github.com/yorkie-team/yorkie/issues/2083).

## Problem

`server/profiling/server.go` registers `pprof.Cmdline` at
`/debug/pprof/cmdline` whenever `PprofEnabled` is true. That handler writes
`os.Args` verbatim. Any credential supplied as a flag — the admin token
signing key (`--backend-secret-key`), the MongoDB connection URI (which
carries the database password), the cluster RPC secret — is therefore handed
to anyone who can reach the profiling port. The port is not authenticated,
and the `yorkie-cluster` chart enables pprof by default, so the exposure is
reachable in the shipped deployment.

The chart already moves its credentials to environment variables to dodge
this, but the server itself still serves the route, and nothing stops an
operator from passing a flag.

## Scope

Command-line disclosure through the private profiling mux only. Not in
scope: authenticating the profiling port, redesigning Helm secret transport,
or a general pprof access policy.

## Plan

1. Drop the `pprof.Cmdline` registration; answer that exact route with a
   `404 Unknown profile`, the same shape the stdlib uses for a profile it
   does not know.
2. Wrap the `/debug/pprof/` subtree handler (`pprof.Index`) in the same
   refusal, normalizing the path first. The index is the fallback for every
   path the exact patterns miss — trailing slash, repeated slashes, dot
   segments, percent-escapes — so the check has to live there too rather
   than relying on `pprof.Index` happening to 404 on an unknown name.
3. Leave every other route alone: `profile`, `symbol`, `trace`, `heap`,
   `goroutine`, `threadcreate`, `block`, `mutex`, and `/metrics`.
4. Tests (internal, so the mux itself is exercised): a RED regression over
   direct / index-fallback / path variants with synthetic credential
   sentinels planted in `os.Args` and restored afterwards, plus a check
   that heap and metrics still answer, with `PprofEnabled` both true and
   false.
5. Document the compatibility change: `server/config.sample.yml` and the
   chart comments that cite the endpoint.

## Acceptance

- [x] `PprofEnabled` true and false: no request can read a sentinel.
- [x] Direct, index/fallback and path variants covered.
- [x] Metrics and heap unchanged.
- [x] Tests restore `os.Args`; no inspection of live processes.
- [x] Regression fails before the fix; `make lint` and the focused package
      test (`-race`) pass.

**Archived**: 2026-10-13 — acceptance met by #2200 (closes #2083); boxes ticked at archive.
