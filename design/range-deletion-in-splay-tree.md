---
title: delete-range-in-splay-tree
target-version: X.X.X
---
---

# Range Deletion in Splay Tree

## Summary

In Splay Tree, an access for a node make the node the root of tree. So when deleting many nodes like `deleteNodes` in `pkg/json/rga_tree_split.go`, deletion for each node in Splay Tree calls many unnecessary `rotation`s.

Using the propertiy of Splay Tree that Change the root freely, 

### Goals

The function `deleteRange` should separate all nodes exactly in the given range as a subtree. After the function ends, the entire tree from and weight of every node must be correct just as when the nodes were deleted one by one.

## Proposal Details

This is where we detail how to use the feature with snippet or API and describe
the internal implementation.

### Risks and Mitigation

`deleteRange` does not consider the occurrence of new nodes from Due to concurrent editing in the range to be deleted. They should be filtered before using `deleteRange`, and `deleteRange` should be executed continuously in the smaller ranges that does not include them.
