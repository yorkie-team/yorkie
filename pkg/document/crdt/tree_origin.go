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

package crdt

import (
	"github.com/yorkie-team/yorkie/pkg/document/resource"
	"github.com/yorkie-team/yorkie/pkg/document/time"
)

// TreeOrigins makes local Tree inserts record insertion origins. It is a
// test hook: tests turn it off to play a client that predates origins and
// check that mixed documents still converge. Nothing else changes it.
var TreeOrigins = true

// TreeInsertOrigin is the context a Tree insertion was made in, the Tree
// counterpart of InsertOrigin. Both sides are sibling boundaries inside one
// parent:
//   - Left is the boundary the content was inserted after, in canonical form:
//     nil for the start of the parent, the element's ID after an element, and
//     (createdAt, k) after character k-1 of a text insertion.
//   - Right is the ID of the sibling that was immediately to the right of the
//     caret, tombstones included, or nil for the end of the parent. Inside a
//     text node it is the continuation (createdAt, k).
//
// A node without one (legacy content, element split products) was placed by
// the createdAt-only rule. A scan stops at such a node, except at an element
// split product, which it passes over (see isSplitProduct). A node moved by a
// merge keeps the origin it was inserted with.
type TreeInsertOrigin struct {
	Left  *TreeNodeID
	Right *TreeNodeID

	// parent is the declared parent of an insertion being placed: what a nil
	// Left is the start of. Runtime only; a recorded node's parent is its
	// MergedFrom, or the parent it sits in.
	parent *TreeNodeID

	// cut marks the origin of an element split with no content: it resolves
	// to the cut point (see splitCutLeft) rather than to an insertion slot.
	// Runtime only.
	cut bool

	// vv is the version vector of a remote edit being placed, nil for a
	// local one. Runtime only.
	vv time.VersionVector
}

// unknownSplitTail returns the children of the split products of parent that
// the edit did not know about, in order. A split the author had not seen cut
// the parent after the author's caret had already chosen its place, so for
// that edit the parent's end is still the end of the last such product.
func (t *Tree) unknownSplitTail(parent *TreeNode, vv time.VersionVector) []*TreeNode {
	if len(vv) == 0 || parent.IsText() {
		return nil
	}
	var tail []*TreeNode
	for id := parent.InsNextID; id != nil; {
		sib := t.findFloorNode(id)
		if sib == nil || !sib.id.Equal(id) || sib.IsText() || time.TicketKnown(vv, sib.id.CreatedAt) {
			break
		}
		tail = append(tail, sib.Children(true)...)
		id = sib.InsNextID
	}
	return tail
}

// splitCutLeft returns the node an element split cuts after: the sibling
// right before the split's right origin. Every sibling between the split's
// left boundary and its right origin is concurrent with the split, and the
// insertions among them were ordered by origin relative to that right
// origin, so cutting right before it keeps each of them on the side the
// insert rule puts it on a replica that saw the split first. A right origin
// that is no longer a child here (the end, or moved by a concurrent split)
// cuts at the end.
func (t *Tree) splitCutLeft(
	parent, left *TreeNode, origin *TreeInsertOrigin,
) (*TreeNode, resource.DataSize, error) {
	var diff resource.DataSize
	children := parent.Children(true)
	start := 0
	if left != parent {
		start = -1
		for i, c := range children {
			if c == left {
				start = i + 1
				break
			}
		}
		if start < 0 {
			return left, diff, nil
		}
	}
	rightIdx, children, d, err := t.originRightIn(children, start, origin.Right)
	diff.Add(d)
	if err != nil {
		return nil, diff, err
	}
	if left != parent {
		for i, c := range children {
			if c == left {
				start = i + 1
				break
			}
		}
	}
	switch {
	case rightIdx > start:
		return children[rightIdx-1], diff, nil
	case rightIdx == start:
		return left, diff, nil
	case len(children) > start:
		return children[len(children)-1], diff, nil
	default:
		return left, diff, nil
	}
}

func sameTreeBoundary(a, b *TreeNodeID) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return a.Equal(b)
}

// sameLeft reports whether sibling o was inserted at the same left boundary
// as the insertion being placed. "The start of a parent" names a parent: a
// merge moves a parent's children into another one, so two nil lefts only
// match when they are the start of the same parent -- o's MergedFrom when a
// merge moved it here, otherwise the parent it sits in.
func sameLeft(o *TreeNode, parent *TreeNode, origin *TreeInsertOrigin) bool {
	if o.Origin.Left != nil || origin.Left != nil {
		return sameTreeBoundary(o.Origin.Left, origin.Left)
	}
	if origin.parent == nil {
		return true
	}
	own := parent.id
	if o.MergedFrom != nil {
		own = o.MergedFrom
	}
	return own.Equal(origin.parent)
}

// boundaryAfter is the canonical boundary right after node.
func boundaryAfter(node *TreeNode) *TreeNodeID {
	if node.IsText() {
		return &TreeNodeID{CreatedAt: node.id.CreatedAt, Offset: node.id.Offset + node.Length()}
	}
	return node.id
}

// canonicalLeft resolves the left origin of an insertion at pos: nil at the
// start of the parent, otherwise the boundary after the left sibling. An
// element anchor's offset in a TreePos is not canonical (toTreePos adds the
// child index to it), so the element's own ID is used instead.
func (t *Tree) canonicalLeft(pos *TreePos) (*TreeNodeID, bool) {
	parent, left := t.ToTreeNodes(pos)
	if parent == nil || left == nil {
		return nil, false
	}
	if parent == left {
		return nil, true
	}
	if left.IsText() {
		return pos.LeftSiblingID, true
	}
	return left.id, true
}

// LocalTreeOrigin returns the origin a local insert at pos records. It must be
// called before the edit runs.
func (t *Tree) LocalTreeOrigin(pos *TreePos) (*TreeInsertOrigin, bool) {
	parent, left := t.ToTreeNodes(pos)
	if parent == nil || left == nil {
		return nil, false
	}
	origin := &TreeInsertOrigin{}
	realParent := parent
	if parent == left {
		children := realParent.Index.Children(true)
		if len(children) > 0 {
			origin.Right = children[0].Value.id
		}
		return origin, true
	}
	if left.Index.Parent != nil {
		realParent = left.Index.Parent.Value
	}
	if left.IsText() {
		origin.Left = pos.LeftSiblingID
		k := pos.LeftSiblingID.Offset
		if k > left.id.Offset && k < left.id.Offset+left.Length() {
			// Inside the text piece: the next character continues it.
			origin.Right = &TreeNodeID{CreatedAt: left.id.CreatedAt, Offset: k}
			return origin, true
		}
	} else {
		origin.Left = left.id
	}
	if next := realParent.Index.NextSiblingOf(left.Index); next != nil {
		origin.Right = next.Value.id
	}
	return origin, true
}

// originLeftNode resolves a canonical left boundary to the node that ends at
// it, or nil for the start of the parent or a node no longer present.
func (t *Tree) originLeftNode(id *TreeNodeID) *TreeNode {
	if id == nil {
		return nil
	}
	node := t.findFloorNode(id)
	if node == nil || !node.IsText() || id.Offset == 0 {
		return node
	}
	return t.findFloorNode(&TreeNodeID{CreatedAt: id.CreatedAt, Offset: id.Offset - 1})
}

// originRightIn returns the index in children of the piece that starts at
// id, splitting a text piece that covers it, or -1 when it is not a child
// of this parent (end of the parent, or moved away by a split or merge).
func (t *Tree) originRightIn(
	children []*TreeNode, from int, id *TreeNodeID,
) (int, []*TreeNode, resource.DataSize, error) {
	var diff resource.DataSize
	if id == nil {
		return -1, children, diff, nil
	}
	node := t.findFloorNode(id)
	if node == nil {
		return -1, children, diff, nil
	}
	if node.IsText() && node.id.Offset < id.Offset && id.Offset < node.id.Offset+node.Length() {
		d, err := node.Split(t, id.Offset-node.id.Offset, nil, nil)
		if err != nil {
			return -1, children, diff, err
		}
		diff.Add(d)
		node = t.findFloorNode(id)
		if node == nil || node.Index.Parent == nil {
			return -1, children, diff, nil
		}
		children = node.Index.Parent.Value.Children(true)
	}
	for i := from; i < len(children); i++ {
		if children[i] == node {
			return i, children, diff, nil
		}
	}
	return -1, children, diff, nil
}

// integrateTree returns the node a new insertion goes after among the
// children of parent, starting from left (parent itself for the start),
// using both origins: the YjsMod / FugueMax rule RGATreeSplit.integrate
// applies to Text, over one parent's child list. Only siblings between the
// left boundary and the right origin can be concurrent with the insertion.
func (t *Tree) integrateTree(
	parent, left *TreeNode,
	origin *TreeInsertOrigin,
	editedAt *time.Ticket,
) (*TreeNode, resource.DataSize, error) {
	var diff resource.DataSize
	children := parent.Children(true)
	start := 0
	if left != parent {
		start = -1
		for i, c := range children {
			if c == left {
				start = i + 1
				break
			}
		}
		if start < 0 {
			return left, diff, nil
		}
	}

	rightIdx, children, d, err := t.originRightIn(children, start, origin.Right)
	diff.Add(d)
	if err != nil {
		return nil, diff, err
	}
	if left != parent {
		// A split of the right origin may have reshaped the slice.
		for i, c := range children {
			if c == left {
				start = i + 1
				break
			}
		}
	}
	if rightIdx == start {
		return left, diff, nil
	}
	if rightIdx < 0 {
		if tail := t.unknownSplitTail(parent, origin.vv); len(tail) > 0 {
			children = append(append([]*TreeNode{}, children...), tail...)
		}
		return t.integrateTreeOpen(parent, left, children, start, origin, editedAt), diff, nil
	}

	order := make(map[*TreeNode]int, rightIdx-start+2)
	order[left] = start - 1
	for i := start; i <= rightIdx; i++ {
		order[children[i]] = i
	}
	right := children[rightIdx]

	compareRight := func(id *TreeNodeID) int {
		if id == nil {
			return 1
		}
		if id.Equal(right.id) {
			return 0
		}
		n := t.findFloorNode(id)
		idx, ok := order[n]
		if !ok || n == left {
			return 1
		}
		if n == right {
			// Inside the right piece but past its start: after it.
			return 1
		}
		if idx < rightIdx {
			return -1
		}
		return 1
	}

	dest, prev := left, left
	scanning := false
	for i := start; ; i++ {
		if !scanning {
			dest = prev
		}
		if i >= rightIdx {
			break
		}
		o := children[i]
		prev = o
		if o.Origin == nil {
			if isSplitProduct(o) {
				continue
			}
			break
		}
		if sameLeft(o, parent, origin) {
			switch cmp := compareRight(o.Origin.Right); {
			case cmp < 0:
				scanning = true
			case cmp == 0:
				if editedAt.ActorID().Compare(o.id.CreatedAt.ActorID()) < 0 {
					return dest, diff, nil
				}
				scanning = false
			default:
				scanning = false
			}
			continue
		}
		oleft := t.originLeftNode(o.Origin.Left)
		if _, inWindow := order[oleft]; !inWindow || oleft == nil {
			break
		}
		if order[oleft] >= i {
			break
		}
	}
	return dest, diff, nil
}

// integrateTreeOpen places an insertion that has no right origin in this
// parent, as RGATreeSplit.integrateOpen does.
func (t *Tree) integrateTreeOpen(
	parent, left *TreeNode,
	children []*TreeNode,
	start int,
	origin *TreeInsertOrigin,
	editedAt *time.Ticket,
) *TreeNode {
	seen := map[*TreeNode]struct{}{left: {}}
	dest, prev := left, left
	scanning := false
	for i := start; ; i++ {
		if !scanning {
			dest = prev
		}
		if i >= len(children) {
			break
		}
		o := children[i]
		if origin.Right != nil && o.id.Equal(origin.Right) {
			// The scan crossed into a split product and reached the right
			// origin there: the window ends at it.
			break
		}
		prev = o
		seen[o] = struct{}{}
		if o.Origin == nil {
			if isSplitProduct(o) {
				continue
			}
			break
		}
		if sameLeft(o, parent, origin) {
			// A right origin that is not a child of this parent -- the end,
			// or a sibling a concurrent element split moved into the new
			// element -- bounds nothing here. Two such siblings are equally
			// open, whichever replica saw the split first, so they tie.
			if t.rightBoundsIn(o, o.Origin.Right, origin.Right) {
				scanning = true
				continue
			}
			if editedAt.ActorID().Compare(o.id.CreatedAt.ActorID()) < 0 {
				return dest
			}
			scanning = false
			continue
		}
		oleft := t.originLeftNode(o.Origin.Left)
		if _, ok := seen[oleft]; !ok || oleft == nil {
			break
		}
	}
	return dest
}

// isSplitProduct reports whether node is the right half of an element split.
// The split logic keeps such a node next to the element it was split from,
// so the scan passes over it as it does over a node hanging inside the
// window, instead of stopping at it like an origin-less legacy node.
func isSplitProduct(node *TreeNode) bool {
	return !node.IsText() && node.InsPrevID != nil
}

// rightBoundsIn reports whether sibling o's right origin bounds it inside
// o's own parent when the inserting node's right origin (mine) does not.
func (t *Tree) rightBoundsIn(o *TreeNode, right, mine *TreeNodeID) bool {
	if right == nil || (mine != nil && right.Equal(mine)) {
		return false
	}
	node := t.findFloorNode(right)
	return node != nil && node.Index.Parent != nil && o.Index.Parent != nil &&
		node.Index.Parent == o.Index.Parent
}

// assignTreeOrigins records origins on inserted content: the first top-level
// node gets the operation's origin, each later one continues after the
// previous, and descendants form a chain inside their own parent.
func assignTreeOrigins(contents []*TreeNode, origin *TreeInsertOrigin) {
	var prev *TreeNode
	for _, content := range contents {
		left := origin.Left
		if prev != nil {
			left = boundaryAfter(prev)
		}
		content.Origin = &TreeInsertOrigin{Left: left, Right: origin.Right}
		assignChildOrigins(content)
		prev = content
	}
}

func assignChildOrigins(node *TreeNode) {
	if node.IsText() {
		return
	}
	var prev *TreeNode
	for _, child := range node.Index.Children(true) {
		c := child.Value
		var left *TreeNodeID
		if prev != nil {
			left = boundaryAfter(prev)
		}
		c.Origin = &TreeInsertOrigin{Left: left}
		assignChildOrigins(c)
		prev = c
	}
}
