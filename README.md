 # yorkie

 ## Features
  - Building blocks for an optimistic replication style system that ensures eventual consistency
  - Providing offline editing and real-time automatic synchronization
  - Using the document(JSON-like) as the basic data type
  - Stored documents can be searchable(the documents can be editable after attaching)

 ## Concept layout

 ```
  +--Client "A" (User)---------+
  | +--Document "D-1"--------+ |            +--Agent-------------------------+
  | | { a: 1, b: [], c: {} } | <-- CRDT --> | +--Collection "C-1"----------+ |
  | +------------------------+ |            | | +--Document "D-1"--------+ | |      +--Mongo DB---------------+
  +----------------------------+            | | | { a: 1, b: [], c: {} } | | |      | Snapshot for query      |
                                            | | +------------------------+ | | <--> | Snapshot with CRDT Meta |
  +--Client "A" (User)---------+            | | +--Document "D-2"--------+ | |      | Operations              |
  | +--Document "D-1"--------+ |            | | | { a: 1, b: [], c: {} } | | |      +-------------------------+
  | | { a: 2, b: [], c: {} } | <-- CRDT --> | | +------------------------+ | |
  | +------------------------+ |            | +----------------------------+ |
  +----------------------------+            +--------------------------------+
                                                             ^
  +--Client "C" (Admin)--------+                             |
  | +--Query "Q-1"-----------+ |                             |
  | | db.['c-1'].find(...)   | <-- Find Query ---------------+
  | +------------------------+ |
  +----------------------------+
 ```

## SDKs
 - JS SDK: https://github.com/hackerwins/yorkie-js-sdk

## Internals

 - yorkie is based on [ H.-G. Roh, M. Jeon, J.-S. Kim, and J. Lee, “Replicated abstract
data types: Building blocks for collaborative applications,” J. Parallel
Distrib. Comput., vol. 71, no. 3, pp. 354–368, Mar. 2011. [Online]](http://csl.skku.edu/papers/jpdc11.pdf).
