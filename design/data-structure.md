---
title: data-structure
target-version: 0.4.6
---

# Data Structures

This document covers the data structures of the SDK.

## Summary

The `json` and `crdt` package has data structures for representing the contents of JSON-like documents edited by the user.
This document explains what data structures are used for and how they refer to each other.

### Goals

This document is to help new SDK contributors understand the overall data structures of the JSON-like document.

### Non-Goals

This document does not describe algorithms in distributed systems such as [CRDT](https://en.wikipedia.org/wiki/Conflict-free_replicated_data_type)s or [logical clock](https://en.wikipedia.org/wiki/Logical_clock)s.

## Proposal Details

The `json` and `crdt` package has data structures for representing the contents of JSON-like documents edited by the user.

### Overview

Below is the dependency graph of data structures used in a JSON-like document.


<img src="https://github.com/yorkie-team/yorkie/assets/78714820/d91aebad-4637-4873-803e-cda025ade1f6" width="700" />


The data structures can be divided into three groups:

- JSON-like: Data structures used directly in JSON-like documents.
- CRDT: Data structures used by JSON-like group to resolve conflicts.
- Common: Data structures used for general purposes.

The data structures of each group have the dependencies shown in the figure above; the data structure on the left side of an arrow use the data structure on the right.

### JSON-like Group

JSON-like data strucutres are used when editing JSON-like documents.

- `Primitive`: represents primitive data like `string`, `number`, `boolean`, `null`, etc.
- `Object`: represents [object type](https://developer.mozilla.org/en-US/docs/Web/JavaScript/Reference/Global_Objects/Object) of JavaScript. Just like JavaScript, you can use `Object` as [hash table](https://en.wikipedia.org/wiki/Hash_table).
- `Array`: represents [array type](https://developer.mozilla.org/en-US/docs/Web/JavaScript/Reference/Global_Objects/Array) of JavaScript. You can also use `Array` as [list](https://en.wikipedia.org/wiki/List_(abstract_data_type)).
- `Text`: represents text with style attributes in rich text editors such as [Quill](https://quilljs.com/). Users can express styles such as bold, italic, and underline to text content. Of course, it can represent just a plain text in text-based editors such as [CodeMirror](https://codemirror.net). It supports collaborative editing; multiple users can modify parts of the contents without conflict.
- `Counter`: represents a counter in the document. As a proxy for the CRDT counter, it is used when the user manipulates the counter from the outside.
- `Tree`: represents CRDT-based tree structure that is used to represent the document tree of text-based editor such as [ProseMirror](https://prosemirror.net/).

JSON-like data structures can be edited through proxies. For example:

```js
doc.update((root) => {
  // set a `Primitive<string>` "world" to the root `object` at key "hello".
  root.hello = 'world'; // { "hello": "world" }

  // set an `array` [1, 2, 3] to the root `object` at key "array".
  root.array = [1, 2, 3]; // { "hello": "world", "array": [1, 2, 3] }

  // push a `Primitive<number>` 4 to the `array` at the end.
  root.array.push(4); // { "hello": "world", "array": [1, 2, 3, 4] }
});
```

The code above uses `Primitive`, `Object` and `Array` in JSON-like group.

### CRDT Group

CRDT data structures are used by JSON-like group to resolve conflicts in concurrent editing.

- `RHT`(Replicated Hash Table): similar to hash table, but resolves concurrent-editing conflicts.
- `ElementRHT`: similar to `RHT`, but has elements as values.
- `RGATreeList`: extended `RGA(Replicated Growable Array)` with an additional index tree. The index tree manages the indices of elements and provides faster access to elements at the int-based index.
- `RGATreeSplit`: extended `RGATreeList` allowing characters to be represented as blocks rather than each single character.
- `CRDTTree`: represents the CRDT tree with an index tree structure'. It resolves conflicts arising from concurrent editing.
### Common Group

Common data structures can be used for general purposes.

- [`SplayTree`](https://en.wikipedia.org/wiki/Splay_tree): A tree that moves nodes to the root by splaying. This is effective when user frequently access the same location, such as text editing. We use `SplayTree` as an index tree to give each node a weight, and to quickly access the node based on the index.
- [`LLRBTree`](https://en.wikipedia.org/wiki/Left-leaning_red%E2%80%93black_tree): A tree simpler than Red-Black Tree. Newly added `floor` method finds the node of the largest key less than or equal to the given key.
- [`Trie`](https://en.wikipedia.org/wiki/Trie): A data structure that can quickly search for prefixes of sequence data such as strings. We use `Trie` to remove nested events when the contents of the `Document` are modified at once.
- `IndexTree`: A tree implementation to represent a document of text-based editors.
  
### Risks and Mitigation

We can replace the data structures with better ones for some reason, such as performance. For example, `SplayTree` used in `RGATreeList` can be replaced with [TreeList](https://commons.apache.org/proper/commons-collections/apidocs/org/apache/commons/collections4/list/TreeList.html).
