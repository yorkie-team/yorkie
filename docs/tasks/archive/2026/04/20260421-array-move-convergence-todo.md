# Array Move Convergence Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Fix Array.MoveAfter convergence by replacing movedFrom+cascade with Kleppmann's LWW position register.

**Architecture:** Separate element identity from position in RGATreeList. Each element gets a stable identity (ElementEntry) pointing to a physical position slot (the existing RGATreeListNode). Move creates a new position node and LWW-updates the element's pointer. Dead position nodes (abandoned by moves) have splay tree weight 0.

**Tech Stack:** Go, protobuf, splay tree

**Design doc:** `docs/design/array-move-convergence.md`

---

## File Map

| File | Action | Responsibility |
|------|--------|---------------|
| `pkg/document/crdt/rga_tree_list.go` | Major modify | Core: add ElementEntry, refactor MoveAfter, remove movedFrom+cascade |
| `pkg/document/crdt/rga_tree_list_test.go` | Major modify | Unit tests for new MoveAfter behavior |
| `pkg/document/crdt/array.go` | Minor modify | Update DeepCopy, Nodes, Elements for new structure |
| `pkg/document/crdt/element.go` | No change | Element interface stays the same |
| `pkg/document/operations/move.go` | No change | Move operation struct unchanged |
| `api/yorkie/v1/resources.proto` | Minor modify | Add pos_moved_at to RGANode |
| `api/converter/to_bytes.go` | Minor modify | Serialize dead nodes + pos_moved_at |
| `api/converter/from_bytes.go` | Minor modify | Deserialize dead nodes + pos_moved_at |
| `api/converter/to_pb.go` | No change | Move operation converter unchanged |
| `api/converter/from_pb.go` | No change | Move operation converter unchanged |
| `test/integration/array_test.go` | Minor modify | Remove t.Skip from convergence tests, add counterexample test |

---

### Task 1: Add ElementEntry and refactor RGATreeListNode

Introduce the element-position separation in the core data structure.

**Files:**
- Modify: `pkg/document/crdt/rga_tree_list.go:27-117`

- [x] **Step 1: Add ElementEntry struct**

Add after the RGATreeListNode struct (around line 35):

```go
// ElementEntry is the stable identity of an element in the list.
// It tracks which position node the element currently occupies
// and the LWW timestamp of the most recent move.
type ElementEntry struct {
	elem         Element
	positionNode *RGATreeListNode
	posMovedAt   *time.Ticket
}
```

- [x] **Step 2: Refactor RGATreeListNode to act as a position node**

Replace the current struct (lines 27-34):

```go
// RGATreeListNode is a position node in the RGA linked list.
// When elementEntry is nil, this is a dead slot abandoned by a move.
type RGATreeListNode struct {
	indexNode    *splay.Node[*RGATreeListNode]
	elementEntry *ElementEntry

	prev *RGATreeListNode
	next *RGATreeListNode
}
```

Remove the `movedFrom` field entirely.

- [x] **Step 3: Add elementMapByCreatedAt to RGATreeList**

Update the struct (lines 112-117):

```go
type RGATreeList struct {
	dummyHead            *RGATreeListNode
	last                 *RGATreeListNode
	nodeMapByIndex       *splay.Tree[*RGATreeListNode]
	nodeMapByCreatedAt   map[string]*RGATreeListNode
	elementMapByCreatedAt map[string]*ElementEntry
}
```

Update `NewRGATreeList()` to initialize the new map.

- [x] **Step 4: Update accessor methods on RGATreeListNode**

Update `Element()`, `CreatedAt()`, `PositionedAt()`, `Len()`, `isRemoved()`, `String()` to work through `elementEntry`:

```go
func (n *RGATreeListNode) Element() Element {
	if n.elementEntry == nil {
		return nil
	}
	return n.elementEntry.elem
}

func (n *RGATreeListNode) CreatedAt() *time.Ticket {
	if n.elementEntry != nil {
		return n.elementEntry.elem.CreatedAt()
	}
	// Dead nodes need their own identity for the linked list.
	// Use the indexNode or store createdAt separately.
	return nil
}

func (n *RGATreeListNode) Len() int {
	if n.elementEntry == nil || n.isRemoved() {
		return 0
	}
	return 1
}

func (n *RGATreeListNode) isRemoved() bool {
	if n.elementEntry == nil {
		return true
	}
	return n.elementEntry.elem.RemovedAt() != nil
}
```

Note: Dead position nodes need a `createdAt` for the `nodeMapByCreatedAt` lookup. Add a `createdAt *time.Ticket` field to `RGATreeListNode` for this purpose — set during `insertAfter` from the `executedAt` of the move that created the position.

- [x] **Step 5: Remove movedFrom-related methods**

Delete `MovedFrom()` and `SetMovedFrom()` (lines 82-89).

- [x] **Step 6: Update InsertAfter to create ElementEntry**

In `insertAfter` (lines 352-370), after creating the new node, also create an ElementEntry and link them:

```go
func (a *RGATreeList) insertAfter(
	prevCreatedAt *time.Ticket,
	value Element,
	executedAt *time.Ticket,
) (*RGATreeListNode, error) {
	prevNode, err := a.findNextBeforeExecutedAt(prevCreatedAt, executedAt)
	if err != nil {
		return nil, err
	}

	entry := &ElementEntry{
		elem: value,
	}
	newNode := newRGATreeListNodeAfter(prevNode, entry)
	entry.positionNode = newNode

	if prevNode == a.last {
		a.last = newNode
	}

	a.nodeMapByIndex.InsertAfter(prevNode.indexNode, newNode.indexNode)
	a.nodeMapByCreatedAt[value.CreatedAt().Key()] = newNode
	a.elementMapByCreatedAt[value.CreatedAt().Key()] = entry
	return newNode, nil
}
```

- [x] **Step 7: Update Add to work with new structure**

Verify `Add()` still works through `InsertAfter` — it should, as it delegates to `InsertAfter`.

- [x] **Step 8: Compile and verify no build errors**

Run: `cd 03_projects/yorkie && go build ./...`
Expected: BUILD SUCCESS (tests may fail, that's ok)

- [x] **Step 9: Commit**

```bash
git add pkg/document/crdt/rga_tree_list.go
git commit -m "Add ElementEntry and refactor RGATreeListNode for position separation"
```

---

### Task 2: Rewrite MoveAfter with LWW semantics

Replace the current release+insertAfter+cascade with LWW position register.

**Files:**
- Modify: `pkg/document/crdt/rga_tree_list.go:242-285, 317-335`

- [x] **Step 1: Remove cascade loop and movedFrom backtracking**

Delete the cascade loop (lines 270-282) and the movedFrom backtracking in `findNextBeforeExecutedAt` (lines 326-328).

- [x] **Step 2: Rewrite MoveAfter**

```go
func (a *RGATreeList) MoveAfter(prevCreatedAt, createdAt, executedAt *time.Ticket) error {
	prevNode, ok := a.nodeMapByCreatedAt[prevCreatedAt.Key()]
	if !ok {
		return fmt.Errorf("MoveAfter prev %s: %w", prevCreatedAt.Key(), ErrChildNotFound)
	}

	entry, ok := a.elementMapByCreatedAt[createdAt.Key()]
	if !ok {
		return fmt.Errorf("MoveAfter target %s: %w", createdAt.Key(), ErrChildNotFound)
	}

	if prevCreatedAt.Key() == createdAt.Key() {
		return nil
	}

	// LWW: skip if a newer move already won
	if entry.posMovedAt != nil && !executedAt.After(entry.posMovedAt) {
		return nil
	}

	// Create new position node after prevNode
	newPosNode, err := a.insertPositionAfter(prevNode.CreatedAt(), executedAt)
	if err != nil {
		return err
	}

	// Detach element from old position (make it dead)
	oldPosNode := entry.positionNode
	oldPosNode.elementEntry = nil
	a.nodeMapByIndex.Splay(oldPosNode.indexNode) // update weight to 0

	// Attach element to new position
	newPosNode.elementEntry = entry
	entry.positionNode = newPosNode
	entry.posMovedAt = executedAt
	entry.elem.SetMovedAt(executedAt)
	a.nodeMapByIndex.Splay(newPosNode.indexNode) // update weight

	return nil
}
```

- [x] **Step 3: Add insertPositionAfter helper**

This creates a bare position node (no element) using the existing forward skip logic:

```go
func (a *RGATreeList) insertPositionAfter(
	prevCreatedAt *time.Ticket,
	executedAt *time.Ticket,
) (*RGATreeListNode, error) {
	prevNode, err := a.findNextBeforeExecutedAt(prevCreatedAt, executedAt)
	if err != nil {
		return nil, err
	}

	newNode := newRGATreeListNodeAfter(prevNode, nil) // nil elementEntry = dead initially
	newNode.createdAt = executedAt // position node's own identity

	if prevNode == a.last {
		a.last = newNode
	}

	a.nodeMapByIndex.InsertAfter(prevNode.indexNode, newNode.indexNode)
	a.nodeMapByCreatedAt[executedAt.Key()] = newNode
	return newNode, nil
}
```

- [x] **Step 4: Simplify findNextBeforeExecutedAt**

Remove the movedFrom backtracking, keep only forward skip:

```go
func (a *RGATreeList) findNextBeforeExecutedAt(
	createdAt *time.Ticket,
	executedAt *time.Ticket,
) (*RGATreeListNode, error) {
	node, ok := a.nodeMapByCreatedAt[createdAt.Key()]
	if !ok {
		return nil, fmt.Errorf("findNextBeforeExecutedAt %s: %w", createdAt.Key(), ErrChildNotFound)
	}

	for node.next != nil && node.next.PositionedAt().After(executedAt) {
		node = node.next
	}

	return node, nil
}
```

- [x] **Step 5: Update release to handle dead nodes**

The `release` function (lines 337-350) needs to work with position nodes that have no element:

```go
func (a *RGATreeList) release(node *RGATreeListNode) {
	if a.last == node {
		a.last = node.prev
	}

	node.prev.next = node.next
	if node.next != nil {
		node.next.prev = node.prev
	}
	node.prev, node.next = nil, nil

	a.nodeMapByIndex.Delete(node.indexNode)
	if node.createdAt != nil {
		delete(a.nodeMapByCreatedAt, node.createdAt.Key())
	}
	if node.elementEntry != nil {
		delete(a.nodeMapByCreatedAt, node.elementEntry.elem.CreatedAt().Key())
	}
}
```

- [x] **Step 6: Compile and verify no build errors**

Run: `cd 03_projects/yorkie && go build ./...`

- [x] **Step 7: Commit**

```bash
git add pkg/document/crdt/rga_tree_list.go
git commit -m "Rewrite MoveAfter with LWW position register semantics"
```

---

### Task 3: Update Marshal, Nodes, DeepCopy, and related methods

Methods that traverse the linked list need to handle dead position nodes.

**Files:**
- Modify: `pkg/document/crdt/rga_tree_list.go` (Marshal, Nodes, Set, Delete, Get)
- Modify: `pkg/document/crdt/array.go` (DeepCopy, Elements, Descendants)

- [x] **Step 1: Update Marshal to skip dead nodes**

```go
func (a *RGATreeList) Marshal() string {
	sb := strings.Builder{}
	sb.WriteString("[")

	current := a.dummyHead.next
	isFirst := true
	for current != nil {
		if current.elementEntry != nil && !current.isRemoved() {
			if isFirst {
				isFirst = false
			} else {
				sb.WriteString(",")
			}
			sb.WriteString(current.elementEntry.elem.Marshal())
		}
		current = current.next
	}

	sb.WriteString("]")
	return sb.String()
}
```

- [x] **Step 2: Update Nodes to skip dead nodes**

```go
func (a *RGATreeList) Nodes() []*RGATreeListNode {
	var nodes []*RGATreeListNode
	current := a.dummyHead.next
	for current != nil {
		if current.elementEntry != nil {
			nodes = append(nodes, current)
		}
		current = current.next
	}
	return nodes
}
```

- [x] **Step 3: Update Array.DeepCopy**

In `array.go` (lines 134-149), update to work with the new structure. `Nodes()` already filters dead nodes, so `DeepCopy` should work as-is if `node.elem` is accessed through the accessor. Verify and adjust if needed.

- [x] **Step 4: Update FindPrevCreatedAt to skip dead nodes**

```go
func (a *RGATreeList) FindPrevCreatedAt(createdAt *time.Ticket) (*time.Ticket, error) {
	node, ok := a.nodeMapByCreatedAt[createdAt.Key()]
	if !ok {
		return nil, fmt.Errorf("FindPrevCreatedAt %s: %w", createdAt.Key(), ErrChildNotFound)
	}

	for {
		node = node.prev
		if a.dummyHead == node || (node.elementEntry != nil && !node.isRemoved()) {
			break
		}
	}

	return node.CreatedAt(), nil
}
```

- [x] **Step 5: Compile and verify**

Run: `cd 03_projects/yorkie && go build ./...`

- [x] **Step 6: Commit**

```bash
git add pkg/document/crdt/rga_tree_list.go pkg/document/crdt/array.go
git commit -m "Update traversal methods to handle dead position nodes"
```

---

### Task 4: Unit tests for MoveAfter convergence

Write tests that verify convergence under different application orders.

**Files:**
- Modify: `pkg/document/crdt/rga_tree_list_test.go`

- [x] **Step 1: Write test for basic LWW move**

Two concurrent moves of the same element — higher timestamp wins:

```go
func TestRGATreeListMoveAfterLWW(t *testing.T) {
	root := helper.TestRoot()
	ctx := helper.TextChangeContext(root)

	elements := crdt.NewRGATreeList()
	// Create [A, B, C]
	a, _ := crdt.NewPrimitive("A", ctx.IssueTimeTicket())
	b, _ := crdt.NewPrimitive("B", ctx.IssueTimeTicket())
	c, _ := crdt.NewPrimitive("C", ctx.IssueTimeTicket())
	_ = elements.Add(a)
	_ = elements.Add(b)
	_ = elements.Add(c)
	assert.Equal(t, `["A","B","C"]`, elements.Marshal())

	t1 := ctx.IssueTimeTicket() // lower
	t2 := ctx.IssueTimeTicket() // higher

	// Order 1: move(A after C) @t1, then move(A after B) @t2
	_ = elements.MoveAfter(c.CreatedAt(), a.CreatedAt(), t1)
	_ = elements.MoveAfter(b.CreatedAt(), a.CreatedAt(), t2)
	result1 := elements.Marshal()

	// Reset and apply in reverse order
	// ... (rebuild list and apply t2 first, t1 second)
	// Assert same result
}
```

- [x] **Step 2: Run test to verify it fails (before implementation is complete)**

Run: `cd 03_projects/yorkie && go test ./pkg/document/crdt/ -run TestRGATreeListMoveAfterLWW -v`

- [x] **Step 3: Write test for the counterexample from the issue comment**

Three concurrent ops: move(A after C), move(B after A), insert(X after B) — must converge regardless of order.

- [x] **Step 4: Write test for move + delete convergence**

Concurrent move and delete of the same element.

- [x] **Step 5: Run all tests**

Run: `cd 03_projects/yorkie && go test ./pkg/document/crdt/ -v`
Expected: ALL PASS

- [x] **Step 6: Commit**

```bash
git add pkg/document/crdt/rga_tree_list_test.go
git commit -m "Add unit tests for MoveAfter LWW convergence"
```

---

### Task 5: Update protobuf and snapshot serialization

Dead position nodes need to be serialized in snapshots.

**Files:**
- Modify: `api/yorkie/v1/resources.proto:227-230`
- Modify: `api/converter/to_bytes.go:218-231`
- Modify: `api/converter/from_bytes.go:165-189`

- [x] **Step 1: Update RGANode proto message**

```protobuf
message RGANode {
  RGANode next = 1;
  JSONElement element = 2;
  TimeTicket pos_moved_at = 3;
  bool is_dead_position = 4;
  TimeTicket position_created_at = 5;
}
```

- [x] **Step 2: Regenerate protobuf**

Run: `cd 03_projects/yorkie && make proto`

- [x] **Step 3: Update toRGANodes**

In `to_bytes.go`, serialize dead position nodes alongside live ones:

```go
func toRGANodes(rgaNodes []*crdt.RGATreeListNode) ([]*api.RGANode, error) {
	var pbRGANodes []*api.RGANode
	for _, rgaNode := range rgaNodes {
		if rgaNode.Element() == nil {
			// Dead position node
			pbRGANodes = append(pbRGANodes, &api.RGANode{
				IsDeadPosition:    true,
				PositionCreatedAt: ToTimeTicket(rgaNode.PositionCreatedAt()),
			})
			continue
		}

		pbElem, err := toJSONElement(rgaNode.Element())
		if err != nil {
			return nil, err
		}
		pbRGANodes = append(pbRGANodes, &api.RGANode{
			Element:    pbElem,
			PosMovedAt: ToTimeTicket(rgaNode.ElementEntry().PosMovedAt()),
		})
	}
	return pbRGANodes, nil
}
```

Note: `Nodes()` currently filters dead nodes. We need an `AllNodes()` method that includes dead nodes for serialization.

- [x] **Step 4: Update fromJSONArray to handle dead nodes**

In `from_bytes.go`:

```go
func fromJSONArray(pbArr *api.JSONElement_JSONArray) (*crdt.Array, error) {
	elements := crdt.NewRGATreeList()
	for _, pbNode := range pbArr.Nodes {
		if pbNode.IsDeadPosition {
			posCreatedAt, err := fromTimeTicket(pbNode.PositionCreatedAt)
			if err != nil {
				return nil, err
			}
			elements.AddDeadPosition(posCreatedAt)
			continue
		}

		elem, err := fromJSONElement(pbNode.Element)
		if err != nil {
			return nil, err
		}
		posMovedAt, err := fromTimeTicket(pbNode.PosMovedAt)
		if err != nil {
			return nil, err
		}
		if err = elements.AddWithPosMovedAt(elem, posMovedAt); err != nil {
			return nil, err
		}
	}
	// ... rest of array creation
}
```

- [x] **Step 5: Add AllNodes, AddDeadPosition, AddWithPosMovedAt to RGATreeList**

```go
// AllNodes returns all nodes including dead position nodes (for serialization).
func (a *RGATreeList) AllNodes() []*RGATreeListNode {
	var nodes []*RGATreeListNode
	current := a.dummyHead.next
	for current != nil {
		nodes = append(nodes, current)
		current = current.next
	}
	return nodes
}

// AddDeadPosition adds a dead position node during snapshot restoration.
func (a *RGATreeList) AddDeadPosition(createdAt *time.Ticket) { ... }

// AddWithPosMovedAt adds an element with a pre-set posMovedAt (for snapshot restoration).
func (a *RGATreeList) AddWithPosMovedAt(elem Element, posMovedAt *time.Ticket) error { ... }
```

- [x] **Step 6: Compile and verify**

Run: `cd 03_projects/yorkie && make proto && go build ./...`

- [x] **Step 7: Commit**

```bash
git add api/yorkie/v1/resources.proto api/converter/to_bytes.go api/converter/from_bytes.go pkg/document/crdt/rga_tree_list.go
git commit -m "Update protobuf and snapshot serialization for dead position nodes"
```

---

### Task 6: Update GC to purge dead position nodes

Dead position nodes should be garbage collected when all clients have synced past.

**Files:**
- Modify: `pkg/document/crdt/rga_tree_list.go` (purge method)
- Modify: `pkg/document/crdt/root.go` (GC registration)

- [x] **Step 1: Register dead position nodes for GC**

In MoveAfter, after marking old position as dead, register it for GC:

```go
// In MoveAfter, after oldPosNode.elementEntry = nil:
// Register dead node for GC using executedAt as the removal timestamp
```

- [x] **Step 2: Add purge support for dead position nodes**

```go
func (a *RGATreeList) PurgeDeadPosition(createdAt *time.Ticket) error {
	node, ok := a.nodeMapByCreatedAt[createdAt.Key()]
	if !ok {
		return fmt.Errorf("PurgeDeadPosition %s: %w", createdAt.Key(), ErrChildNotFound)
	}
	if node.elementEntry != nil {
		return fmt.Errorf("PurgeDeadPosition: node is not dead")
	}
	a.release(node)
	return nil
}
```

- [x] **Step 3: Verify GC works with existing VersionVector mechanism**

Check that `root.GarbageCollect()` properly iterates and purges dead position nodes.

- [x] **Step 4: Commit**

```bash
git add pkg/document/crdt/rga_tree_list.go pkg/document/crdt/root.go
git commit -m "Add GC support for dead position nodes"
```

---

### Task 7: Integration tests — convergence verification

Remove skips from existing convergence tests and add the counterexample test.

**Files:**
- Modify: `test/integration/array_test.go:409-478`

- [x] **Step 1: Update TestComplicatedArrayConcurrency**

Change `t.Skipf` to `t.Fatalf` or use `assert.True` — these tests must now pass:

```go
if !syncClientsThenCheckEqual(t, []clientAndDocPair{{c0, d0}, {c1, d1}}) {
	t.Fatalf("convergence failed for operation %s", op.opName)
}
```

- [x] **Step 2: Add counterexample test**

Add the three-concurrent-ops test from the issue comment:

```go
func TestArrayMoveAfterCounterexample(t *testing.T) {
	// Initial: [A, B, C]
	// Op1 @t1: move(A, after C)
	// Op2 @t2: move(B, after A)   (t1 < t2)
	// Op3 @t3: insert(X, after B) (t1 < t3 < t2)
	// Must converge regardless of sync order.
}
```

- [x] **Step 3: Run integration tests**

Run: `cd 03_projects/yorkie && go test ./test/integration/ -run TestComplicatedArrayConcurrency -tags integration -v`
Run: `cd 03_projects/yorkie && go test ./test/integration/ -run TestArrayMoveAfterCounterexample -tags integration -v`
Expected: ALL PASS

- [x] **Step 4: Run full test suite**

Run: `cd 03_projects/yorkie && make test`
Expected: ALL PASS

- [x] **Step 5: Commit**

```bash
git add test/integration/array_test.go
git commit -m "Update integration tests for Array.MoveAfter convergence"
```

---

### Task 8: Run lint and final verification

**Files:** None (verification only)

- [x] **Step 1: Run linter**

Run: `cd 03_projects/yorkie && make lint`
Expected: PASS

- [x] **Step 2: Run full test suite**

Run: `cd 03_projects/yorkie && make test`
Expected: ALL PASS

- [x] **Step 3: Run integration tests**

Run: `cd 03_projects/yorkie && make test -tags integration`
Expected: ALL PASS (if MongoDB available)

- [x] **Step 4: Review all changes**

Run: `cd 03_projects/yorkie && git diff main --stat`
Verify no unintended file changes.
