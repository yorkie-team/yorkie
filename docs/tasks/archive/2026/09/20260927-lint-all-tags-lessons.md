**Created**: 2026-09-27

# Lint every build tag — lessons

## An unchecked error in a test is a finding, not noise

`errcheck` in the tag-gated suites looked like 40 lines of `defer
cli.Deactivate(ctx)` hygiene. Wrapping them in `assert.NoError` turned two
integration tests red: `TestDocPresence` and `TestRESTAPI` had been decoding
the admin REST responses with `encoding/json` into `types.DocumentSummary`,
whose `Presences` is `map[string]presence.Data`, while the wire carries the
proto shape `{"<client>": {"data": {...}}}`. The decode failed on every call.
It went unnoticed because `encoding/json` fills in the map *keys* before it
reports the value error, and the tests only asserted on keys — so they passed
with the error dropped on the floor.

Fixed by decoding the way a client does: `protojson` into the response
message, then the converter. The shared type also decodes `SearchDocuments`,
which carries `totalCount`, hence `DiscardUnknown`.

Rule: when a linter asks you to check an error, check it with an assertion
and run the test. Suppressing it (`_ =`) would have kept the false green.

## `ineffassign` found two tests that assert nothing they claim

- `FindDeactivateCandidates return lastClientID test` assigned the returned
  cursor and never looked at it — the assertion was dropped in #1492 when
  pagination moved from projects to clients. Restored: activate one more
  client than a page holds, check the first cursor is set and the next page's
  cursor is strictly after it, deactivate them so the following subtest's
  candidate count is unaffected.
- `revision_test` fetched the initial `docInfo` "to check serverSeq changes"
  and never compared it. It now asserts the later `ServerSeq` is greater.

## `unused` found a security helper that had quietly been superseded

`api/converter.dropSplitLinksInElement` carried a comment about not trusting
wire-supplied split links — the kind of function whose disappearance from the
call graph would be alarming. History showed it was superseded by
`crdt.DropSplitLinksInElement`, which `sanitizeElement` calls. Deleting the
orphan was safe; checking before deleting was the point.

`gcParentsAreComparable` is the opposite case: never called by design, it is a
compile-time assertion. `var _ = gcParentsAreComparable` keeps it compiled and
tells `unused` so.

## Mechanical rewrites still need a compiler in the loop

`fmt.Fprintf(sb, …)` in place of `sb.WriteString(fmt.Sprintf(…))` does not
compile when `sb` is a `strings.Builder` value rather than a pointer; the
rewrite needs `&sb`. Letting the vet errors drive the second pass found every
one.

## A refactor of a script under test has to keep its tests' fixture working

`scripts/go-fix.sh` gained a guard suite on main (a stubbed `go`, a throwaway
repository) after this branch's plan was written. Moving the tag derivation
into `scripts/go-build-tags.sh` and calling it as `scripts/go-build-tags.sh`
broke all ten cases: the fixture repository has no `scripts/`. The caller now
resolves its sibling through `BASH_SOURCE` before it `cd`s.

## Judgement calls

- `QF1008` (drop the embedded field from a selector) is off: the json package
  writes `p.Array.X` / `p.Object.X` on purpose, to say which layer a call
  lands in. 45 findings, all of that shape.
- `SA1019` on the deprecated Watch RPC types is excluded for
  `server/rpc/yorkie_server.go` only: those handlers exist to serve clients
  that predate `WatchRequest`.
- `lll`, `goconst`, `gocyclo` are off in the tag-gated suites under `test/`
  (`integration`, `bench`, `complex`, `fuzz`) — every finding of the three was
  there, in golden strings, fixture keys and case tables. `test/helper` is
  ordinary code and stays linted.

## Self review, round 1 (correctness / test adequacy)

Ten findings, nine applied. The pattern across most of them: the first pass
made each test *check its error*, and the review asked whether it then checks
the *right thing*.

- The six `assert.Panics(t, fn, ErrX)` blocks passed `ErrX` as a message
  label, so any panic satisfied them. Now `assert.PanicsWithError`; a
  deliberately wrong expected error fails, so the check is real.
- The fixed REST decode still only asserted on presence keys — the exact
  blind spot that hid the broken decode. Values are compared now.
- The restored cursor assertion only said "later than", which a cursor
  pointing at the wrong element of the page would satisfy. It now pins the
  exact client ID of each page and the empty page after the last.
- `revision_test`'s restored `Greater(initial)` was implied by the line after
  it; it is now exact equality with `initial + SnapshotInterval`.
- `DiscardUnknown` gave back the strictness the decode fix was for.
  `SearchDocumentsResponse` is a superset of the GetDocuments/ListDocuments
  responses by JSON name, so decoding into it stays strict.
- A response with no `document` would have panicked inside a `wg.Go`
  goroutine, taking the test binary down; now an error.
- A failed `ActivateClient` would have put a nil in the cleanup list; now
  `require`.
- `CDPATH` could make `go-fix.sh`'s `$here` two lines.
- The pre-commit hook's cost note said ~6s; measured: ~17s cold, 1-2s warm,
  against ~8s cold before.

Declined: moving `GOOS=linux GOARCH=amd64` into `go-build-tags.sh`'s output.
Two callers, each commented, and the go-fix guard suite pins that script's
environment; a shared env layer for two call sites is more to read than the
duplication it removes.

## Self review, round 2 (design fit)

Two findings, both applied:

- `scripts/go-build-tags.sh` decides what both the lint and modernize lanes
  check, but was not in `CI_DEFINING_PATHS`, so a branch could empty the tag
  set and auto-promote with every lane green. Added, with its matcher case.
  Rule: a script that a lane's command reaches is part of the lane — when a
  refactor adds a hop, the new file joins the protected list in the same
  change.
- `path: ^test/` also exempted `test/helper`, which is untagged, ordinary code
  that was linted on main. Narrowed to the four tag-gated suites.

## Self review, round 3 (security/docs)

Three findings, all applied:

- `scripts/go-build-tags.sh` was a new top-level script missing from
  `scripts/README.md`, which the Docs workflow's `verify-doc-index.mjs` fails
  on — a red check `make verify` does not run. Rule: a new file under
  `scripts/` means running `node scripts/verify-doc-index.mjs` locally too.
- The todo and a commit body still said the style linters were off "under
  `test/`" after round 2 narrowed it.
- The platform guard only read `//go:build` lines; a `_windows.go` or
  `_arm64_test.go` restricts a file by name alone and would have dropped out of
  the linux/amd64 analysis silently. The script now checks file names in the
  positions Go reads them (last one or two `_` parts, GOARCH last), with tests
  both ways — and a mutation that disables the guard turns the new test red.

Three rounds, the cap.

