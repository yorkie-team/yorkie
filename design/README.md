# Design Document

## Contents

- [Document Editing](document-editing.md): Local and remote document editing mechanism
- [Peer Awareness](peer-awareness.md): Algorithm for managing end-user status
- [Data Structure](data-structure.md): CRDT data structures in `crdt` package
  - [Range Deletion in Splay Tree](range-deletion-in-splay-tree.md): Rotation-free range deletion algorithm for splay tree
- [PubSub](pub-sub.md): Client-side event sharing with gRPC server-side stream and PubSub pattern
- [Garbage Collection](garbage-collection.md): Deleting unused nodes in CRDT system
  - [Garbage Collection for Text Type](gc-for-text-type.md): Garbage collection for text nodes
  - [Housekeeping](housekeeping.md): Deactivating outdated clients for efficient garbage collection

- [Retention](retention.md): Clearing unnecessary changes with `--backend-snapshot-with-purging-changes` flag
- [Cluster Mode](cluster-mode.md): Multiple-agent cluster mode with ETCD

## Maintaining the Document

For significant scope and complex new features, it is recommended to write a Design Document before starting any implementation work. On the other hand, we don't need to design documentation for small, simple features and bug fixes.

Writing a design document for big features has many advantages:

- It helps new visitors or contributors understand the inner workings or the architecture of the project.
- We can agree with the community before code is written that could waste effort in the wrong direction.

While working on your design, writing code to prototype your functionality may be useful to refine your approach.

Authoring Design document is also proceeded in the same [contribution flow](../CONTRIBUTING.md) as normal Pull Request such as function implementation or bug fixing.
