**Created**: 2026-09-25

# `go fix` modernizations — lessons

## The version bump was the blocker, not the rewrites

#1872 reads as one task with four sub-items, but the sub-items do not share a
dependency on the bump. Three of them (`errors.AsType`, the goroutine leak
profile, the Green Tea GC benchmark) need a Go 1.26 *toolchain* to compile or
to measure. The fourth needs nothing newer than what the repository already
builds with — `strings.SplitSeq`, the newest of the four APIs, shipped in Go
1.24, and `go.mod` already says `go 1.25.0`.

So the split is not "do the easy part first". It is that one of the four items
is genuinely independent of the version, and the other three are not. Landing
it separately means the bump PR, when someone with workflow write access can
open it, is a bump and nothing else.

## `maps.Clone` is not the same rewrite as `maps.Copy`

The tempting form for `pkg/errors/metadata.go`'s `Metadata()` is

```go
return maps.Clone(e.metadata)
```

which is wrong in a way the tests do catch, but only by luck of coverage:
`maps.Clone(nil)` returns `nil`, and the existing code returns a non-nil empty
map for a `MetadataError` whose metadata is nil. Callers ranging over the
result do not care; a caller writing into it does.

`pkg/trie/path_trie.go` rejects `Clone` for a second, unrelated reason — it
sizes the new map `len(node.children)+1` because it is about to insert one
more entry, and `Clone` sizes it exactly. The capacity hint is the point of
the line.

Both sites keep `make` + `maps.Copy`. The rule that came out of it:
`maps.Clone` is only a drop-in when the source is known non-nil *and* nobody
sized the destination on purpose.

## `strings.SplitSeq` only applies when the slice is thrown away

Two of the repository's `strings.Split` calls feed a `range` and never touch
the slice again, so the allocation is pure waste and `SplitSeq` removes it.
Every other `strings.Split` here indexes, re-slices, or returns the result.
The grep that finds the rewritable ones is `range strings.Split(`, not
`strings.Split(` — searching for the latter and filtering by eye is how a
behaviour change gets in.

## `reflect.TypeFor` needs the static type to be the type you meant

`server/backend/database/mongo/registry.go` builds four BSON registry keys from
`reflect.TypeOf(types.ID(""))`-style expressions, where the conversion exists
only to produce a value whose type is the key. `reflect.TypeFor[types.ID]()`
says that directly and drops the throwaway value.

The two test-file `reflect.TypeOf` calls look similar and are not: they pass a
variable whose static type is an interface, and `TypeFor` would key on the
interface rather than the concrete type behind it. Same call, opposite
answer — which is why this rewrite has to be read per site rather than applied
by pattern.
