# Design Document

## Contents

### Document & Presence

- [Document-Client Lifecycle](document-client-lifecycle.md): States and transitions of documents and clients
- [Document Editing](document-editing.md): Local and remote document editing mechanism
- [Data Structure](data-structure.md): Data structures for root in document
- [DocPresence](doc-presence.md): Data structure for presence in document
- [Presence](presence.md): Dedicated presence for real-time user tracking

### CRDT

- [Undo/Redo](undo-redo.md): Per-client undo/redo with reverse operations and position reconciliation
- [VersionVector](version-vector.md): VersionVector for GC and Resolving conflicts in CRDT
- [VV Cleanup](vv-cleanup.md): Remove detached client's lamport from version vectors
- [Garbage Collection](garbage-collection.md): Removing tombstones in CRDT
- [Garbage Collection for Text Type](gc-for-text-type.md): Garbage collection for text type CRDT
- [GC Registration on Set Conflict](gc-registration-on-set-conflict.md): Fix missing GC registration when new element loses LWW conflict
- [Tree](tree.md): Tree data structure for tree-based rich text editor
- [Concurrent Merge and Split](concurrent-merge-split.md): Fix convergence bugs in concurrent tree merge/split operations
- [Range Deletion in SplayTree](range-deletion-in-splay-tree.md): Improving range deletion in SplayTree

### Schema

- [Schema Validation](schema-validation.md): Declarative document structure definition and real-time change validation
- [Tree Schema](tree-schema.md): ProseMirror-compatible structural constraints for yorkie.Tree

### Platform

- [Sharded Cluster Mode](sharded-cluster-mode.md): Shard-based server cluster mode with consistent hashing
- [MongoDB Sharding](mongodb-sharding.md): MongoDB sharding with sharding strategy considerations
- [Membership Management](membership.md): Leader-member structure for managing cluster node lifecycle
- [PubSub](pub-sub.md): Client-side event sharing with gRPC server-side stream and PubSub pattern
- [Housekeeping](housekeeping.md): Background tasks for cleaning up documents and tombstones
- [Document Epoch](document-epoch.md): Epoch-based detection and recovery for clients stale after compaction
- [Fine-grained Document Locking](fine-grained-document-locking.md): Fine-grained document locking for high concurrency
- [OLAP Stack for MAU Tracking](olap-stack.md): OLAP stack for Monthly Active Users (MAU) tracking
- [Cluster Service Authentication](cluster-service-auth.md): Shared secret authentication for inter-node cluster RPCs

## Maintaining the Document

For significant scope and complex new features, it is recommended to write a Design Document before starting any implementation work. On the other hand, we don't need to design documentation for small, simple features and bug fixes.

Writing a design document for big features has many advantages:

- It helps new visitors or contributors understand the inner workings or the architecture of the project.
- We can agree with the community before code is written that could waste effort in the wrong direction.

While working on your design, writing code to prototype your functionality may be useful to refine your approach.

Authoring a design document follows the same [contribution flow](../../CONTRIBUTING.md) as a normal pull request, such as feature implementation or bug fixing.
