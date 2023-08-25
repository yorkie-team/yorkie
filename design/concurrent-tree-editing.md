---
title: concurrent-tree-editing
target-version: 0.4.6
---

# Concurrent Tree Editing

## Summary

In Yorkie, users can create and edit JSON-like documents using JSON-like data structures such as `Primitive`, `Object`, `Array`, `Text`, and `Tree`. Among these, the `Tree` structure is used to represent the document model of a tree-based text editor, similar to XML.

This document introduces the `Tree` data structure, and explains the operations provided by `Tree`, focusing on the `Tree` coordinate system and the logic of the `Tree.Edit` operation. Furthermore, it explains how this logic ensures eventual consistency in concurrent document editing scenarios.

### Goals

This document aims to help new SDK contributors understand the overall `Tree` data structure and explain how Yorkie ensures consistency when multiple clients are editing concurrently.

### Non-Goals

This document focuses on `Tree.Edit` operations rather than `Tree.Style`.

## Proposal Details

### JSON-like Tree

In yorkie, a JSON-like `Tree` is used to represent the document model of a tree-based text editor.

This tree-based document model resembles XML tree and consists of element nodes and text nodes. element nodes can have attributes, and text nodes contain a string as their value. For example:

<img src="https://github.com/yorkie-team/yorkie/assets/78714820/b5500d8b-43db-4d89-983d-5708b7041cc4" width="500" />


**Operation**

The JSON-like `Tree` provides specialized operations tailored for text editing rather than typical operations of a general tree. To specify the operation's range, an `index` is used. For example:

<img src="https://github.com/yorkie-team/yorkie/assets/78714820/88d6acde-6775-4af2-ae32-ba7a6b324abc" width="450" />

These `index`es are assigned in order at positions where the user's cursor can reach. These `index`es draw inspiration from ProseMirror's index and share a similar structural concept.

1. `Tree.Edit`

Users can use the `Edit` operation to insert or delete nodes within the `Tree`.

[코드]

Where `fromIdx` is the starting position of editing, `toIdx` is the ending position, and `contents` represent the nodes to be inserted. If `contents` are omitted, the operation only deletes nodes between `fromIdx` and `toIdx`.

<img src="https://github.com/yorkie-team/yorkie/assets/78714820/c1184839-3e50-41fa-b558-8c0677285660" width="450" />

[코드]

Similarly, users can specify the editing range using a `path` that leads to the `Tree`'s node in the type of `[]int`.

2. `Tree.Style`

Users can use the `Style` operation to specify attributes for the element nodes in the `Tree`.

[코드]

### Implementation of Edit Operation

**Tree Coordinate System**

<img src="https://github.com/yorkie-team/yorkie/assets/78714820/08c1e917-08cf-492c-84c2-cf72b98c38f3" width="600" />

Yorkie implements the above data structure to create a JSON-like `Document`, which consists of different layers, each with its own coordinate system. The dependency graph above can be divided into three main groups. The **JSON-like** group directly used by users to edit JSON-like `Document`s. The **CRDT** Group is utilized from the JSON-like group to resolve conflicts in concurrent editing situations. Finally, the **common** group is used for the detailed implementation of CRDT group and serves general purposes.

Thus, the JSON-like `Tree`, introduced in this document, has dependencies such as **'`Tree` → `CRDTTree` → `IndexTree`'**, and each layer has its own coordinate system:

<img src="https://github.com/yorkie-team/yorkie/assets/78714820/33519a1e-c8cb-4b4d-9d0e-d2fcc2052013" width="450" />

These coordinate systems transform in the order of '`index(path)` → `IndexTree.TreePos` → `CRDTTree.TreeNodeID` → `CRDTTree.TreePos`'.

<img src="https://github.com/yorkie-team/yorkie/assets/78714820/af339dc7-5c03-4cae-a1cb-f5879bfce3be" />

1. `index` → `IndexTree.TreePos`

The `index` is the coordinate system used by users for local editing. This `index` is received from the user, and is converted to `IndexTree.TreePos`. This `IndexTree.TreePos` represents the physical position within the local tree and is used for actual tree editing.

2. `IndexTree.TreePos` → (`CRDTTree.TreeNodeID`) → `CRDTTree.TreePos`

Next, the obtained `IndexTree.TreePos` is transformed into the logical coordinate system of the distributed tree, represented by `CRDTTree.TreePos`. To achieve this, the given physical position, `IndexTree.TreePos`, is used to find the parent node and left sibling node. Then, a `CRDTTree.TreePos` is created using the unique IDs of the parent node and left sibling node, which are `CRDTTree.TreeNodeID`. This coordinate system is used in subsequent `Tree.Edit` and `Tree.Style` operations.

In the case of remote editing, where the local coordinate system is received from the user in local editing, there is no need for Step 1 since changes are pulled from the server using `ChangePack` to synchronize the changes. (For more details: [링크])

**Tree.Edit Logic**

The core process of the `Tree.Edit` operation is as follows:

1. Find `CRDTTree.TreePos` from the given `fromIdx` and `toIdx` (local editing only).
2. Find the corresponding left sibling node and parent node within the `IndexTree` based on `CRDTTree.TreePos`.
3. Delete nodes in the range of `fromTreePos` to `toTreePos`.
4. Insert the given nodes at the appropriate positions (insert operation only).

**[STEP 1]** Find `CRDTTree.TreePos` from the given `fromIdx` and `toIdx` (local editing only) [링크]

In the case of local editing, the given `index`es are converted to `CRDTTree.TreePos`. The detailed process is the same as described in the 'Tree Coordinate System' above.

**[STEP 2]** Find the corresponding left sibling node and parent node within the `IndexTree` based on `CRDTTree.TreePos` [링크]

2-1. For text nodes, if necessary, split nodes at the appropriate positions to find the left sibling node.

2-2. Determine the sequence of nodes and find the appropriate position. Since `Clone`s[링크] of each client might exist in different states, the `findFloorNode` function is used to find the closest node (lower bound).

**[STEP 3]** Delete nodes in the range of `fromTreePos` to `toTreePos` [링크]

3-1. Traverse the range and identify nodes to be removed. If a node is an element node and doesn't include both opening and closing tags, it is excluded from removal.

3-2. Update the `latestCreatedAtMapByActor` information for each node and mark nodes with tombstones in the `IndexTree` to indicate removal.

**[STEP 4]** Insert the given nodes at the appropriate positions (insert operation only) [링크]

4-1. If the left sibling node at the insertion position is the same as the parent node, it means the node will be inserted as the leftmost child of the parent. Hence, the node is inserted at the leftmost position of the parent's children list.

4-2. Otherwise, the new node is inserted to the right of the left sibling node.

### How to Guarantee Eventual Consistency

**Coverage**

<img src="https://github.com/yorkie-team/yorkie/assets/78714820/c911ffb4-9021-4a1f-9a11-b8e28fb41435" width="850" >

Using conditions such as range type, node type, and edit type, 27 possible cases of concurrent editing can be represented.

<img src="https://github.com/yorkie-team/yorkie/assets/78714820/d1054938-b701-4e90-bcbb-8ff5d62b19d4" width="400">

Eventual consistency is guaranteed for these 27 cases. In addition, eventual consistency is ensured for the following edge cases:

- Selecting multiple nodes in a multi-level range
- Selecting only a part of nodes (e.g., selecting only the opening tag or closing tag of the node)

**How does it work?**

- `lastCreatedAtMapByActor`

[코드]

`latestCreatedAtMapByActor` is a map that stores the latest creation time by actor for the nodes included in the editing range. However, relying solely on the typical `lamport` clocks that represent local clock of clients, it's not possible to determine if two events are causally related or concurrent. For instance:

<img src="https://github.com/yorkie-team/yorkie/assets/78714820/cc025542-2c85-40ef-b846-157f38177487" width="450" />

In the case of the example above, during the process of synchronizing operations between clients A and B, client A is unaware of the existence of '`c`' when client B performs `Edit(0,2)`. As a result, an issue arises where the element '`c`', which is within the contained range, gets deleted together.

To address this, the `lastCreatedAtMapByActor` is utilized during operation execution to store final timestamp information for each actor. Subsequently, this information allows us to ascertain the causal relationship between the two events.

- Restricted to only `insertAfter`

[코드]

To ensure consistency in concurrent editing scenarios, only the `insertAfter` operation is allowed, rather than `insertBefore`, similar to conventional CRDT algorithms. To achieve this, `CRDTTree.TreePos` takes a form that includes `LeftSiblingID`, thus always maintaining a reference to the left sibling node.

If the left sibling node is the same as the parent node, it indicates that the node is positioned at the far left of the parent's children list.

- `FindOffset`

[코드]

During the traversal of the given range in `traverseInPosRange` (STEP3), the process of converting the provided `CRDTTree.TreePos` to an `IndexTree.TreePos` is executed. To determine the `offset` for this conversion, the `FindOffset` function is utilized. In doing so, calculating the `offset` excluding the removed nodes prevents potential issues that can arise in concurrent editing scenarios.

### Risks and Mitigation

- In the current conflict resolution policy of Yorkie, when both insert and delete operations occur simultaneously, even if the insert range is included in the delete range, the inserted node remains after synchronization. This might not always reflect the user's intention accurately.

- The `Tree.Edit` logic uses index-based traversal instead of node-based traversal for a clearer implementation. This might lead to a performance impact. If this becomes a concern, switching to node-based traversal can be considered.
