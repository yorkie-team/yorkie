# Yorkie

Yorkie is a framework for building collaborative editing applications.

  - Optimistic replication style system that ensures eventual consistency
  - Providing real-time synchronization and offline editing
  - Using the JSON-like document(CRDT) as the basic data type
  - Stored documents can be searchable then the documents can be editable after attaching

## How Yorkie works

 ```
  +--Client "A" (Go)----+
  | +--Document "D-1"-+ |               +--Agent------------------+
  | | { a: 1, b: {} } | <-- Changes --> | +--Collection "C-1"---+ |
  | +-----------------+ |               | | +--Document "D-1"-+ | |      +--Mongo DB---------------+
  +---------------------+               | | | { a: 1, b: {} } | | |      | Changes                 |
                                        | | +-----------------+ | | <--> | Snapshot with CRDT Meta |
  +--Client "B" (JS)----+               | | +--Document "D-2"-+ | |      | Snapshot for query      |
  | +--Document "D-1"-+ |               | | | { a: 1, b: {} } | | |      +-------------------------+
  | | { a: 2, b: {} } | <-- Changes --> | | +-----------------+ | |
  | +-----------------+ |               | +---------------------+ |
  +---------------------+               +-------------------------+
                                                     ^
  +--Client "C" (JS)------+                          |
  | +--Query "Q-1"------+ |                          |
  | | db.['C-1'].find() | <-- MongoDB query ---------+
  | +-------------------+ |
  +-----------------------+
 ```

 - Clients can have a replica of the document representing an application model locally on several devices.
 - Each client can independently update the document on their local device, even while offline.
 - When a network connection is available, Yorkie figures out which changes need to be synced from one device to another, and brings them into the same state.
 - If the document was changed concurrently on different devices, Yorkie automatically syncs the changes, so that every replica ends up in the same state with resolving conflict.

## Agent and SDKs
 - Agent: https://github.com/yorkie-team/yorkie
 - JS SDK: https://github.com/yorkie-team/yorkie-js-sdk
 - Go Client: https://github.com/yorkie-team/yorkie/tree/master/client

## Internals

 - Yorkie is based on [ H.-G. Roh, M. Jeon, J.-S. Kim, and J. Lee, “Replicated abstract
data types: Building blocks for collaborative applications,” J. Parallel
Distrib. Comput., vol. 71, no. 3, pp. 354–368, Mar. 2011. [Online]](http://csl.skku.edu/papers/jpdc11.pdf).
