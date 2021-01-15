---
title: gc-for-text-type
target-version: 0.1.1
---

# Garbage collection for Text Type

## Summary

When all synchronization between clients is complete, the node marked tombstone is no longer needed.
As the number of nodes marked tombstone increases, unnecessary memory waste occurs.
So it provides garbage collection to solve the problem of wasting memory.

### Goals

Implements garbage collection for text types, Text, and RichText. When
executing garbage collection of a document, text nodes marked with
tombstones are also deleted.

### Non-Goals

1. It does not directly control the garbage collection provided by the programming language. It simply removes the relationship of the nodes connected to the linked list and makes it a collection target.
2. It is not a function that can be called directly by the yorkie user.

## Proposal details

### How dose it work?

If there is a node that is deleted when the text is modified, store the text element at `Root` instance.
At this time, it is stored inside `Document` provided to the user and inside `Document` used in internal logic.
That's because the two `Document` use separate memory addresses, and even the same text values have different memory addresses.

And when the client synchronizes, it calls `GarbageCollect()` inside the `ApplyChangePack()` method.
When `GarbageCollect()` is called, the node to be deleted internally is excluded from the linked list.
What you need to know at this time is that `RGATreeSplitNode` has `insPrev` and `insNext`.
Must also do the work to connect the relationship between `insPrev` and `insNext`.
> `insPrev` and `insNext` are used to remember other nodes connected to the insert.
> For example, when `abc` is divided into `a`, `b`, `c`, it looks like this: [abc] divided to [a]<->[b]<->[c]
