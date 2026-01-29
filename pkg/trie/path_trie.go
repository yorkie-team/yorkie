/*
 * Copyright 2026 The Yorkie Authors. All rights reserved.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package trie

import (
	"sync"
	"sync/atomic"
)

const (
	// rootSegment is the segment identifier for the root node
	rootSegment = ""
)

// PathTrie is a concurrent trie with lock-free reads that uses path segments as keys.
// It uses immutable data structures with copy-on-write semantics for concurrent access.
//
// Key features:
// - Lock-free reads: Multiple goroutines can read simultaneously without blocking
// - Path copying writes: Only nodes in the modification path are copied
// - Path-based keys: Keys are arrays of string segments (e.g., ["project-1", "room-5"])
// - Memory efficient: Old tree versions are garbage collected quickly
type PathTrie[T any] struct {
	root atomic.Value // *trieNode[T] (immutable tree)
	mu   sync.Mutex   // Serializes writes only
}

// trieNode represents a single node in the path trie.
// All fields are immutable once created.
type trieNode[T any] struct {
	segment  string                  // e.g., "channel-1", "room-5", "user-100"
	value    T                       // Value stored at this node (zero value if not set)
	hasValue bool                    // Whether this node has a value
	children map[string]*trieNode[T] // Immutable map of child nodes
}

// NewPathTrie creates a new lock-free path trie.
func NewPathTrie[T any]() *PathTrie[T] {
	t := &PathTrie[T]{}
	t.root.Store(&trieNode[T]{
		segment:  rootSegment,
		children: make(map[string]*trieNode[T]),
	})
	return t
}

// Insert adds or updates a value in the trie.
// keyPath should be the key split by segments.
// Example: ["project-1", "room-5", "user-100"]
func (t *PathTrie[T]) Insert(keyPath []string, value T) {
	if len(keyPath) == 0 {
		return
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	oldRoot := t.root.Load().(*trieNode[T])
	newRoot := t.insertNode(oldRoot, keyPath, 0, value)
	t.root.Store(newRoot)
}

// insertNode recursively creates a new tree with the inserted value.
// It only copies nodes along the insertion path (path copying).
func (t *PathTrie[T]) insertNode(
	node *trieNode[T],
	keyPath []string,
	depth int,
	value T,
) *trieNode[T] {
	if depth == len(keyPath) {
		// Leaf node: set value
		return &trieNode[T]{
			segment:  node.segment,
			value:    value,
			hasValue: true,
			children: node.children, // Reuse existing children
		}
	}

	segment := keyPath[depth]

	// Copy children map
	newChildren := make(map[string]*trieNode[T], len(node.children)+1)
	for k, v := range node.children {
		newChildren[k] = v
	}

	// Recursively insert into child
	child := node.children[segment]
	if child == nil {
		child = &trieNode[T]{
			segment:  segment,
			children: make(map[string]*trieNode[T]),
		}
	}
	newChildren[segment] = t.insertNode(child, keyPath, depth+1, value)

	return &trieNode[T]{
		segment:  node.segment,
		value:    node.value,
		hasValue: node.hasValue,
		children: newChildren,
	}
}

// Get retrieves a value by its key path.
// This operation is completely lock-free.
// Returns the value and true if found, zero value and false otherwise.
func (t *PathTrie[T]) Get(keyPath []string) (T, bool) {
	root := t.root.Load().(*trieNode[T])
	return t.navigateToNode(root, keyPath, 0)
}

// GetOrInsert atomically retrieves a value or inserts a new one if it doesn't exist.
// The create function is called only if the value doesn't exist.
// This operation is thread-safe.
func (t *PathTrie[T]) GetOrInsert(keyPath []string, create func() T) T {
	var zero T
	if len(keyPath) == 0 {
		return zero
	}

	// Fast path: try lock-free read first
	if val, ok := t.Get(keyPath); ok {
		return val
	}

	// Slow path: need to insert
	t.mu.Lock()
	defer t.mu.Unlock()

	// Double-check after acquiring lock (another goroutine might have inserted)
	oldRoot := t.root.Load().(*trieNode[T])
	if val, ok := t.navigateToNode(oldRoot, keyPath, 0); ok {
		return val
	}

	// Create new value and insert
	newValue := create()
	newRoot := t.insertNode(oldRoot, keyPath, 0, newValue)
	t.root.Store(newRoot)

	return newValue
}

// navigateToNode is a helper for navigating to a node and retrieving its value.
// This method can be used both with and without holding a lock.
func (t *PathTrie[T]) navigateToNode(node *trieNode[T], keyPath []string, depth int) (T, bool) {
	var zero T
	if node == nil {
		return zero, false
	}

	if depth == len(keyPath) {
		return node.value, node.hasValue
	}

	segment := keyPath[depth]
	child := node.children[segment]
	if child == nil {
		return zero, false
	}

	return t.navigateToNode(child, keyPath, depth+1)
}

// Delete removes a value from the trie.
// Empty parent nodes are automatically cleaned up.
// Returns true if a value was deleted, false if the path didn't exist.
func (t *PathTrie[T]) Delete(keyPath []string) bool {
	if len(keyPath) == 0 {
		return false
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	oldRoot := t.root.Load().(*trieNode[T])

	// Check if the path exists before attempting deletion
	if _, exists := t.navigateToNode(oldRoot, keyPath, 0); !exists {
		return false
	}

	newRoot := t.deleteNode(oldRoot, keyPath, 0)
	// Always update root, even if it becomes empty (with no value but may have children)
	if newRoot != nil {
		t.root.Store(newRoot)
	} else {
		// If root becomes nil, create an empty root
		t.root.Store(&trieNode[T]{
			segment:  rootSegment,
			children: make(map[string]*trieNode[T]),
		})
	}

	return true
}

// deleteNode recursively creates a new tree with the value removed.
// It cleans up empty parent nodes automatically.
func (t *PathTrie[T]) deleteNode(
	node *trieNode[T],
	keyPath []string,
	depth int,
) *trieNode[T] {
	if node == nil {
		return nil
	}

	if depth == len(keyPath) {
		// Leaf node: clear value
		newNode := &trieNode[T]{
			segment:  node.segment,
			hasValue: false,
			children: node.children,
		}

		// If node becomes empty, return nil so parent can remove it
		if len(newNode.children) == 0 {
			return nil
		}
		return newNode
	}

	segment := keyPath[depth]
	child := node.children[segment]
	if child == nil {
		return node // Path not found, no change
	}

	// Recursively delete from child
	newChild := t.deleteNode(child, keyPath, depth+1)

	// Copy children map
	newChildren := make(map[string]*trieNode[T], len(node.children))
	for k, v := range node.children {
		if k == segment {
			if newChild != nil {
				newChildren[k] = newChild
			}
			// If newChild is nil, we skip adding it (deletion)
		} else {
			newChildren[k] = v
		}
	}

	// Clean up empty nodes, but keep root node even if empty
	if !node.hasValue && len(newChildren) == 0 && node.segment != rootSegment {
		return nil
	}

	return &trieNode[T]{
		segment:  node.segment,
		value:    node.value,
		hasValue: node.hasValue,
		children: newChildren,
	}
}

// ForEach traverses all values in the trie.
// This operation is lock-free and can run concurrently with writes.
// The callback function receives each value. Return false to stop iteration.
func (t *PathTrie[T]) ForEach(fn func(T) bool) {
	root := t.root.Load().(*trieNode[T])
	if root != nil {
		t.traverseNode(root, fn)
	}
}

// traverseNode performs DFS traversal of the trie.
func (t *PathTrie[T]) traverseNode(node *trieNode[T], fn func(T) bool) bool {
	if node.hasValue {
		if !fn(node.value) {
			return false
		}
	}

	for _, child := range node.children {
		if !t.traverseNode(child, fn) {
			return false
		}
	}

	return true
}

// ForEachDescendant traverses all descendant values of the given key path.
// This is useful for hierarchical queries like "get all users in a room".
// This operation is lock-free.
func (t *PathTrie[T]) ForEachDescendant(
	keyPath []string,
	fn func(T) bool,
) {
	root := t.root.Load().(*trieNode[T])
	if root == nil {
		return
	}

	// Navigate to the starting node
	node := root
	for _, segment := range keyPath {
		if node.children == nil {
			return
		}
		child := node.children[segment]
		if child == nil {
			return
		}
		node = child
	}

	// Traverse from this node
	t.traverseNode(node, fn)
}

// Len returns the total number of values in the trie.
// This operation is lock-free but requires full tree traversal.
func (t *PathTrie[T]) Len() int {
	count := 0
	t.ForEach(func(T) bool {
		count++
		return true
	})
	return count
}

// GetRoot returns the value stored at the root node (empty path).
// This operation is lock-free.
func (t *PathTrie[T]) GetRoot() (T, bool) {
	root := t.root.Load().(*trieNode[T])
	return root.value, root.hasValue
}

// InsertRoot adds or updates the value at the root node (empty path).
// This operation is thread-safe.
func (t *PathTrie[T]) InsertRoot(value T) {
	t.mu.Lock()
	defer t.mu.Unlock()

	oldRoot := t.root.Load().(*trieNode[T])
	newRoot := &trieNode[T]{
		segment:  oldRoot.segment,
		value:    value,
		hasValue: true,
		children: oldRoot.children,
	}
	t.root.Store(newRoot)
}

// GetOrInsertRoot atomically retrieves the root value or inserts a new one if it doesn't exist.
// The create function is called only if the value doesn't exist.
// This operation is thread-safe.
func (t *PathTrie[T]) GetOrInsertRoot(create func() T) T {
	// Fast path: try lock-free read first
	if val, ok := t.GetRoot(); ok {
		return val
	}

	// Slow path: need to insert
	t.mu.Lock()
	defer t.mu.Unlock()

	// Double-check after acquiring lock
	oldRoot := t.root.Load().(*trieNode[T])
	if oldRoot.hasValue {
		return oldRoot.value
	}

	// Create new value and insert at root
	newValue := create()
	newRoot := &trieNode[T]{
		segment:  oldRoot.segment,
		value:    newValue,
		hasValue: true,
		children: oldRoot.children,
	}
	t.root.Store(newRoot)

	return newValue
}

// DeleteRoot removes the value at the root node.
// Returns true if a value was deleted, false if there was no value.
func (t *PathTrie[T]) DeleteRoot() bool {
	t.mu.Lock()
	defer t.mu.Unlock()

	oldRoot := t.root.Load().(*trieNode[T])
	if !oldRoot.hasValue {
		return false
	}

	var zero T
	newRoot := &trieNode[T]{
		segment:  oldRoot.segment,
		value:    zero,
		hasValue: false,
		children: oldRoot.children,
	}
	t.root.Store(newRoot)

	return true
}
