---
title: gc-for-text-type
target-version: 0.1.1
---

# Garbage Collection for Text Type

## Summary

We only implemented garbage collection for elements marked as tombstones to
reduce unnecessary waste of memory. This feature implements a garbage
collection for text nodes marked as tombstones that occur within elements of
text type.

### Goals

Implements garbage collection for text types, `Text`, and `RichText`. When
executing garbage collection of a document, text nodes marked with tombstones
are also deleted.

### Non-Goals

It does not directly control the garbage collection provided by the programming
language. It simply removes the relationship of the nodes connected to the
linked list and makes it a collection target.

## Proposal Details

### How does it work?

#### Caching text nodes marked as tombstones during editing

Text nodes marked as tombstones during editing are cached to quickly find them
when garbage collection is running.

Text nodes that are deleted are marked as tombstones and cached in a map of the
`RGATreeSplit` instance. Additionally, text elements have text nodes that are
deleted during the editing are cached in a map of the `Root` instance. And the
`Root` instance is managed by the `Document` instance.

Note that the Document manages the original data of the document and the copies
it provides to the user. Since the original data and the clone exist in
different memory spaces, the maps for cache are managed separately in each.

```go
// pkg/document/document.go

type Document struct {
	// doc is the original data of the actual document.
	doc *InternalDocument

	// clone is a copy of `doc` to be exposed to the user and is used to
	// protect `doc`.
	clone *json.Root
}
```

And garbage collection is run on both the original and the clone.

#### Running garbage collection

Garbage collection occurs automatically when documents are synchronized by the
client.

> The important thing is that the user does not call it directly. If the user
> calls it directly, it may cause unintended behavior and lead to problems for
> the entire program. Currently, `Document.GarbageCollect` is exposed to users
> as a public method, so it may be misunderstood. We are aware of this issue
> and are working through issue [#125](https://github.com/yorkie-team/yorkie/issues/125).

It cleans up text nodes marked as tombstones by traversing the modified text
elements stored in the `Root` instance. Organized nodes are excluded from the
linked list and are subject to garbage collection provided by Go runtime.

```bash
# input 'abc'
 +-----+        +-----+        +-----+
 | 'a' |   ->   | 'b' |   ->   | 'c' |
 +-----+        +-----+        +-----+

# remove 'b'
              (tombstoned)
 +-----+        +-----+        +-----+
 | 'a' |   ->   | 'b' |   ->   | 'c' |
 +-----+        +-----+        +-----+

# after running gc
 +-----+        +-----+
 | 'a' |   ->   | 'c' |
 +-----+        +-----+

           (Collection target)
               (tomstone)    
                +-----+
                | 'b' |
                +-----+
```
> It only makes it a garbage collection target. If we use `runtime.GC()`
> directly, performance degradation will occur due to frequent `GC` calls.

What you need to know at this time is that `RGATreeSplitNode` has `insPrev` and
`insNext`. It must also do the work to connect the relationship between
`insPrev` and `insNext`.

![block-wise-rga-structure](media/block-wise-rga-structure.jpg)

> `insPrev` and `insNext` are used to remember other nodes connected to the
> insert. For example, when `abc` is divided into `a`, `b`, `c`, it looks like
> this: [abc] divided to [a]<->[b]<->[c]
