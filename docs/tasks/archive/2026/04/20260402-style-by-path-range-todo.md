**Created**: 2026-04-02
**Completed**: 2026-04-10 (Go SDK)

# Add range-based StyleByPath and RemoveStyleByPath

Related issue: https://github.com/yorkie-team/yorkie-js-sdk/issues/1197

## Summary

Add range signatures to `StyleByPath` / `RemoveStyleByPath`. The CRDT
layer is unchanged — the JSON layer converts paths to positions via
`PathToPos` and delegates to the existing `Style`/`RemoveStyle`
operations.

## Go SDK (this repo)

- [x] `StyleByPath(fromPath, toPath, attributes)` in
      `pkg/document/json/tree.go:274`
- [x] `RemoveStyleByPath(fromPath, toPath, attributesToRemove)` in
      `pkg/document/json/tree.go:321`
- [x] Integration tests in `test/integration/tree_test.go`
      (8 call sites across multiple sub-tests: single range, multi
      range, nested path range, whole-tree range)
- [x] `make lint && make test` green

## JS SDK (different repository)

The JS SDK lives in `yorkie-team/yorkie-js-sdk`, not in this
repository. The original task included JS SDK items as a reminder
but they cannot be tracked here — they belong in that repo's own
task log. Landing and tracking are handled there against the
upstream issue linked above.
