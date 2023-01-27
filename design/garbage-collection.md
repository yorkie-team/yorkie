---
title: garbage-collection
target-version: 0.1.0
---

# Garbage Collection

## Summary

One of the most important issues in CRDT systems is handling tombstones. Tombstones are used to properly synchronize the document even when remote peer's operations refer to the elements that have already been locally deleted. This causes the problem that the document keeps growing even if elements are deleted.

Yorkie provides garbage collection to solve this problem.

### Goals

Implements garbage collection system that purges unused nodes not being referenced remotely. 

### Non-Goals

For text types, garbage collection works slightly differently: refer to [this document](gc-for-text-type.md).

## Proposal Details

### How It Works

Garbage collection checks that deleted nodes are no longer referenced remotely and purges them completely.

Server records the logical timestamp of the last change pulled by the client whenever the client requests PushPull. And Server returns the smallest logical timestamp, `min_synced_seq` of all clients in response PushPull to the client. `min_synced_seq` is used to check whether deleted nodes are no longer to be referenced remotely or not.

An example of garbage collection:

#### State 1

![garbage-collection-1](media/garbage-collection-1.png)

In the initial state, both clients have `"ab"`.

#### State 2

![garbage-collection-2](media/garbage-collection-2.png)

Client `c1` deletes `"b"`, which is recorded as a change with logical timestamp `3`. The text node of `"b"` can be referenced by remote, so it is only marked as tombstone. And the client `c1` sends change `3` to server through PushPull API and receives as a response that `min_synced_seq` is `2`. Since all clients did not receive the deletion `change 3`, the text node is not purged by garbage collection.

Meanwhile, client `c2` inserts `"c"` after textnode `"b"`.

#### State 3

![garbage-collection-3](media/garbage-collection-3.png)

Client `c2` pushes change `4` to server and receives as a response that `min_synced_seq` is `3`. After the client applies change `4`, the contents of document are changed to `ac`. This time, all clients have received change `3`, so textnode `"b"` is completely removed.

#### State 4

![garbage-collection-4](media/garbage-collection-4.png)

Finally, after client `c1` receives change `4` from server, purges textnode `"b"` because it is no longer referenced remotely.
