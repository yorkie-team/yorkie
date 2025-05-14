# Design Document

## Contents

### Document

- [Document Editing](document-editing.md): Local and remote document editing mechanism
- [Document Removal](document-removal.md): Soft deletion of documents
- [Data Structure](data-structure.md): Data structures for root in document
- [Presence](presence.md): Real-time presence in document

### CRDT

- [VersionVector](version-vector.md): VersionVector for GC and Resolving conflicts in CRDT
- [Garbage Collection](garbage-collection.md): Removing tombstones in CRDT
- [Garbage Collection for Text Type](gc-for-text-type.md): Garbage collection for text type CRDT
- [Tree](tree.md): Tree data structure for tree-based rich text editor
- [Range Deletion in SplayTree](range-deletion-in-splay-tree.md): Improving range deletion in SplayTree

### Platform

- [Sharded Cluster Mode](sharded-cluster-mode.md): Shard-based server cluster mode with consistent hashing
- [MongoDB Sharding](mongodb-sharding.md): MongoDB sharding with sharding strategy considerations
- [PubSub](pub-sub.md): Client-side event sharing with gRPC server-side stream and PubSub pattern
- [Housekeeping](housekeeping.md): Deactivating outdated clients for efficient garbage collection
- [OLAP Stack for MAU Tracking](olap-stack.md): OLAP stack for Monthly Active Users (MAU) tracking

## Maintaining the Document

For significant scope and complex new features, it is recommended to write a Design Document before starting any implementation work. On the other hand, we don't need to design documentation for small, simple features and bug fixes.

Writing a design document for big features has many advantages:

- It helps new visitors or contributors understand the inner workings or the architecture of the project.
- We can agree with the community before code is written that could waste effort in the wrong direction.

While working on your design, writing code to prototype your functionality may be useful to refine your approach.

Authoring Design document is also proceeded in the same [contribution flow](../CONTRIBUTING.md) as normal Pull Request such as function implementation or bug fixing.
