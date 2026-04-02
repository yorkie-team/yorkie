**Created**: 2026-04-02

# Add range-based StyleByPath and RemoveStyleByPath

Related issue: https://github.com/yorkie-team/yorkie-js-sdk/issues/1197

## Summary

Add range signatures to `styleByPath` (JS) / `StyleByPath` (Go). The CRDT layer
is unchanged — the JSON layer converts paths to positions via `PathToPos` and
delegates to the existing `Style`/`RemoveStyle` operations.

## Go SDK

- [ ] Add `StyleByPath(fromPath, toPath, attributes)` to `pkg/document/json/tree.go`
- [ ] Add `RemoveStyleByPath(fromPath, toPath, attributesToRemove)` to the same file
- [ ] Add integration tests in `test/integration/tree_test.go`
- [ ] Verify `make lint && make test` passes

## JS SDK

- [ ] Overload `styleByPath`: support both `(path, attrs)` and `(fromPath, toPath, attrs)`
      in `packages/sdk/src/document/json/tree.ts`
- [ ] Add `removeStyleByPath(fromPath, toPath, keys)` to the same file
- [ ] Add integration tests in `packages/sdk/test/integration/tree_test.ts`
- [ ] Verify existing single-path `styleByPath` tests still pass
- [ ] Verify `pnpm lint && pnpm sdk build && pnpm sdk test` passes
