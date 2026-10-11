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

// Split boundary anchors (yorkie-js-sdk#1436). See
// docs/design/split-boundary-anchors.md.
//
// The text of a split family (a node and the products split off it) is one
// RGA sequence. A split is a boundary anchored right before R, the first item
// after its position that its creator knew. Text concurrent with the split
// that lands between the position and R stays on its left (§7.3); boundaries
// anchored before the same R are ordered newest first (§7.8), each with its
// emission (the boundaries anchored before it and its group, everything newer
// than it); a split made inside the group of such a boundary nests under it.
//
// The rules cover the innermost level of a split, the one that divides the
// node holding the position, when that node's children are all text.
// Anything else falls back to orderSameBoundarySplit.

import (
	"github.com/yorkie-team/yorkie/pkg/document/time"
	"github.com/yorkie-team/yorkie/pkg/index"
)

// boundaryKey names an item of a split family's sequence: a node, or the end
// of the family. The zero key names nothing, which is what a node without a
// recorded boundary holds.
type boundaryKey struct {
	id  *TreeNodeID
	end bool
}

func (k boundaryKey) isZero() bool {
	return k.id == nil && !k.end
}

func (k boundaryKey) equal(other boundaryKey) bool {
	if k.end || other.end {
		return k.end == other.end
	}
	if k.id == nil || other.id == nil {
		return k.id == nil && other.id == nil
	}
	return k.id.Equal(other.id)
}

// anchorOf returns the item n's boundary is anchored in front of.
func anchorOf(n *TreeNode) boundaryKey {
	return boundaryKey{id: n.BoundaryAnchor, end: n.BoundaryAtEnd}
}

// nestOf returns the boundary n's boundary nests under.
func nestOf(n *TreeNode) boundaryKey {
	return boundaryKey{id: n.BoundaryNest}
}

// hasElementChild reports whether node holds an element child as the editing
// change saw it: an element whose removal that change knew is not counted.
//
// Element tombstones must not count by themselves. GC purges them on each
// replica's own schedule, so a test of the local child list would switch a
// paragraph between these rules and orderSameBoundarySplit depending on when
// each replica collected. Reading the removal through versionVector instead
// gives the same answer before and after the purge: a node is purged only
// once every replica knows its removal, so every change applied after the
// purge knew it too. A local edit (empty versionVector) knows every removal.
func hasElementChild(node *TreeNode, versionVector time.VersionVector) bool {
	for _, c := range node.Index.Children(true) {
		if c.Value.IsText() {
			continue
		}
		if c.Value.removedAt != nil && time.TicketKnown(versionVector, c.Value.removedAt) {
			continue
		}
		return true
	}
	return false
}

// hasBoundaryRecord reports whether node is the product of an anchored split.
func hasBoundaryRecord(node *TreeNode) bool {
	return node.BoundaryAnchor != nil || node.BoundaryAtEnd
}

func childrenOf(node *TreeNode) []*TreeNode {
	cs := node.Index.Children(true)
	out := make([]*TreeNode, len(cs))
	for i, c := range cs {
		out[i] = c.Value
	}
	return out
}

func indexOfNode(nodes []*TreeNode, node *TreeNode) int {
	for i, n := range nodes {
		if n == node {
			return i
		}
	}
	return -1
}

func boundaryKeyOf(n *TreeNode) boundaryKey {
	if n == nil {
		return boundaryKey{end: true}
	}
	return boundaryKey{id: n.id}
}

// nextBoundaryNode returns the element that follows node in the document as a
// paragraph boundary of its split family, or nil at the end of the family.
func (t *Tree) nextBoundaryNode(node *TreeNode, walker *insNextWalker) *TreeNode {
	if node.InsNextID == nil {
		return nil
	}
	next := t.findFloorNode(node.InsNextID)
	if next == nil || next.IsText() || next.Index.Parent == nil || !walker.visit(next) {
		return nil
	}
	if !t.sharesSplitFamilyParent(node, next) {
		return nil
	}
	// Only products the anchor rules made are part of the sequence. Whether
	// a product holds element children changes as they are removed and
	// purged, and this walk also runs during GC, where no change says which
	// removals count; the record does not change.
	if !hasBoundaryRecord(next) {
		return nil
	}
	return next
}

// advanceAcrossBoundaries continues the RGA scan of step 04 across the
// paragraph boundaries of a split family. A boundary and its group are
// attached in front of the item behind them, so the scan compares the insert
// with that item: a newer item is crossed, group and all, and an older one
// stops the scan in front of the group.
func (t *Tree) advanceAcrossBoundaries(
	parent, left *TreeNode,
	editedAt *time.Ticket,
	versionVector time.VersionVector,
) (*TreeNode, *TreeNode) {
	if len(versionVector) == 0 || hasElementChild(parent, versionVector) {
		return parent, left
	}
	var walker insNextWalker
	walker.visit(parent)

	landParent, landLeft := parent, left
	pc := childrenOf(parent)
	startIdx := 0
	if left != parent {
		startIdx = indexOfNode(pc, left) + 1
	}
	if startIdx < len(pc) {
		return parent, left
	}

	node := t.nextBoundaryNode(parent, &walker)
	var threshold *time.Ticket
	for node != nil {
		createdAt := node.id.CreatedAt
		if threshold == nil || !createdAt.After(threshold) {
			if time.TicketKnown(versionVector, createdAt) {
				break
			}
			if !createdAt.After(editedAt) {
				break
			}
			threshold = createdAt
		}
		children := childrenOf(node)
		i := 0
		stop := false
		for i < len(children) {
			c := children[i]
			ct := c.id.CreatedAt
			if threshold != nil && ct.After(threshold) {
				i++
				continue
			}
			if !ct.After(editedAt) {
				stop = true
				break
			}
			threshold = nil
			landParent, landLeft = node, c
			i++
			for i < len(children) && children[i].id.CreatedAt.After(editedAt) {
				landLeft = children[i]
				i++
			}
			if i < len(children) {
				stop = true
			}
			break
		}
		if stop {
			break
		}
		node = t.nextBoundaryNode(node, &walker)
	}
	return landParent, landLeft
}

// boundaryTok is one item of a split family's sequence past the split
// position that the splitting change did not know: a boundary (text false,
// idx -1) or a text child of the product at node (idx).
type boundaryTok struct {
	text bool
	node *TreeNode
	idx  int
	prev *TreeNode
	t    *time.Ticket
}

// boundaryTail is the unknown part of a split family's sequence between the
// end of the split node and R, the first item the splitting change knew.
type boundaryTail struct {
	toks []boundaryTok
	// rNode and rIdx locate R when it is a text child of a product; rNode is
	// nil when R is a boundary or the end of the family.
	rNode *TreeNode
	rIdx  int
	rKey  boundaryKey
	// last is the last product walked, where the sequence ends if R is a
	// boundary or the end.
	last *TreeNode
	// tokIdx maps a boundary or a text child to its index in toks.
	tokIdx map[*TreeNode]int
}

// at returns the (parent, offset) where a split before toks[i] opens, or
// before R for i == len(toks).
func (b *boundaryTail) at(i int) (*TreeNode, int) {
	if i >= len(b.toks) {
		if b.rNode != nil {
			return b.rNode, b.rIdx
		}
		return b.last, len(childrenOf(b.last))
	}
	tk := b.toks[i]
	if tk.text {
		return tk.node, tk.idx
	}
	return tk.prev, len(childrenOf(tk.prev))
}

// placeSplitBoundary decides where a split of parent at offset opens, and
// what its boundary is anchored to. ok is false when the rule does not apply
// (element children), and the caller falls back to orderSameBoundarySplit.
func (t *Tree) placeSplitBoundary(
	parent *TreeNode,
	declaredParent *TreeNode,
	offset int,
	editedAt *time.Ticket,
	versionVector time.VersionVector,
) (target *TreeNode, off int, anchor, nest boundaryKey, keepFrom *TreeNodeID, ok bool) {
	if parent.Index.Parent == nil || hasElementChild(parent, versionVector) {
		return nil, 0, boundaryKey{}, boundaryKey{}, nil, false
	}
	local := len(versionVector) == 0
	known := func(tk *time.Ticket) bool {
		return local || time.TicketKnown(versionVector, tk)
	}

	// R is in parent: everything before it is concurrent and stays on the
	// left (§7.3).
	seen := seenParentOf(parent, declaredParent, known)
	children := childrenOf(parent)
	// The RGA skip of FindTreeNodesWithSplitText moved the position past
	// newer concurrent text. Text that sits behind a boundary the change saw
	// was not in front of the position for it, so the run a merge it did not
	// know moved in is looked for from where the skip started.
	start := offset
	for start > 0 && !known(children[start-1].id.CreatedAt) && children[start-1].id.CreatedAt.After(editedAt) {
		start--
	}
	for i := start; i < len(children); i++ {
		if src := t.unknownMergeBoundaryAt(parent, children, i, seen, known); src != nil {
			anchor := boundaryKeyOf(src)
			return parent, i, anchor, boundaryNestOf(seen, anchor, known), src.id, true
		}
		if i < offset {
			continue
		}
		if known(children[i].id.CreatedAt) {
			anchor := boundaryKeyOf(children[i])
			return parent, i, anchor, boundaryNestOf(seen, anchor, known), nil, true
		}
	}

	tail := t.boundaryTailOf(parent, seen, known)
	anchor = tail.rKey
	nest = boundaryNestOf(seen, anchor, known)
	if local {
		target, off = tail.at(len(tail.toks))
		return target, off, anchor, nest, nil, true
	}
	target, off = tail.at(t.boundaryInsertIndex(tail, anchor, nest, editedAt))
	target, off = t.liveSplitTarget(tail, parent, target, off)
	return target, off, anchor, nest, nil, true
}

// liveSplitTarget keeps a remote split out of a product that has been removed
// on this replica but that the splitting change did not know: one the
// change's tail walk passed over as concurrent.
//
// A replica that applied the split first opened it while that product was
// live, and the later removal took only the product, so the split opens at the
// end of the nearest live node before it in the family. That holds whether a
// merge moved the product's content (undoing a split) or there was none: the
// merge target is not used, because later splits may have carried the moved
// content on past it.
// Splitting the tombstone instead would mint a product born removed, and the
// paragraph would be lost on this replica only.
func (t *Tree) liveSplitTarget(tail *boundaryTail, parent, target *TreeNode, off int) (*TreeNode, int) {
	if target == parent || !target.IsRemoved() {
		return target, off
	}
	live := parent
	for _, tk := range tail.toks {
		if tk.text {
			continue
		}
		if tk.node == target {
			break
		}
		if !tk.node.IsRemoved() {
			live = tk.node
		}
	}
	if live.IsRemoved() {
		return target, off
	}
	return live, len(childrenOf(live))
}

// unknownMergeBoundaryAt returns the merged-away node whose boundary stood
// in front of children[i] for the splitting change, or nil.
//
// children[i] opens the run a merge moved out of src, and the change did not
// know that merge: for it, src was still a paragraph after the split
// position, so src is R. A replica that applied the split first anchored it
// there through the tail walk, and the merge then moved src's children into
// its product. Reading only the moved child's MergedFrom and MergedAt, which
// every replica holds alike, the replica that applied the merge first records
// the same anchor and splits in front of the same run.
//
// It does not apply when src is the node the change saw as the parent of the
// position (seenParentOf): then the position was inside src, not before it.
func (t *Tree) unknownMergeBoundaryAt(
	parent *TreeNode, children []*TreeNode, i int, seen *TreeNode, known func(*time.Ticket) bool,
) *TreeNode {
	c := children[i]
	if seen != parent {
		return nil
	}
	if c.MergedFrom == nil || c.MergedAt == nil || known(c.MergedAt) {
		return nil
	}
	if !known(c.MergedFrom.CreatedAt) || c.MergedFrom.Equal(seen.id) {
		return nil
	}
	if i > 0 && children[i-1].MergedFrom != nil && children[i-1].MergedFrom.Equal(c.MergedFrom) {
		return nil
	}
	src := t.findMergeNode(c.MergedFrom)
	if src == nil || !hasBoundaryRecord(src) {
		return nil
	}
	// The run must sit where the merge put it, in the node before src: a
	// later split may have carried it on into a product of its own, and a
	// position in that product was after src's boundary for the change.
	if src.mergedInto == nil || !src.mergedInto.Equal(seen.id) {
		return nil
	}
	var walker insNextWalker
	walker.visit(seen)
	for node := t.nextBoundaryNode(seen, &walker); node != nil; node = t.nextBoundaryNode(node, &walker) {
		if node == src {
			return src
		}
	}
	return nil
}

// seenParentOf returns the node the editing change saw as the parent of the
// split position. When a merge the change did not know has taken that node
// away, the position now resolves into the merge target, as undoing a split
// does; the operation still names the node it saw. Nesting is decided from
// there, and the tail walk passes over it, so that the replica that applied
// the merge first agrees with the one that applied the split first.
//
// The children around the position cannot tell this apart: the end of the
// merge target and the start of the merged-away node have the same
// neighbours once the merge has moved the children.
func seenParentOf(parent, declared *TreeNode, known func(*time.Ticket) bool) *TreeNode {
	if declared == nil || declared == parent || declared.IsText() || declared.Index.Parent == nil {
		return parent
	}
	if declared.mergedInto == nil || declared.removedAt == nil || known(declared.removedAt) {
		return parent
	}
	return declared
}

// boundaryNestOf returns the key of the closest boundary before parent, as the
// splitting change saw it, that is anchored before the same item: emissions
// anchored before that item run contiguously up to it, so a split at parent
// is inside that boundary's group. It returns the zero key when there is none.
func boundaryNestOf(parent *TreeNode, anchor boundaryKey, known func(*time.Ticket) bool) boundaryKey {
	sibs := childrenOf(parent.Index.Parent.Value)
	for i := indexOfNode(sibs, parent); i >= 0; i-- {
		k := sibs[i]
		if k.IsText() || !known(k.id.CreatedAt) {
			continue
		}
		// A boundary the change saw removed was not there for it to split
		// inside.
		if k.removedAt != nil && known(k.removedAt) {
			continue
		}
		if anchorOf(k).equal(anchor) {
			return boundaryKeyOf(k)
		}
	}
	return boundaryKey{}
}

// boundaryTailOf walks the split family past the end of parent, collecting
// the boundaries and text the splitting change did not know, up to R.
func (t *Tree) boundaryTailOf(parent, seen *TreeNode, known func(*time.Ticket) bool) *boundaryTail {
	tail := &boundaryTail{rKey: boundaryKey{end: true}, last: parent}
	var walker insNextWalker
	walker.visit(parent)
	for node := t.nextBoundaryNode(parent, &walker); node != nil; node = t.nextBoundaryNode(node, &walker) {
		createdAt := node.id.CreatedAt
		// A boundary the change saw removed is not in its sequence: it is
		// neither R nor a place to open a split. Its tombstone also sits
		// where its removal left it, which differs between replicas.
		gone := known(createdAt) && node.removedAt != nil && known(node.removedAt)
		// The node the change saw as the parent of the position, when a merge
		// it did not know moved the position out of it: its boundary was
		// before the position for the change, so it is not R either.
		if node == seen && node != parent {
			gone = true
		}
		if known(createdAt) && !gone {
			tail.rKey = boundaryKeyOf(node)
			break
		}
		if gone {
			// What is left in it went with it.
			continue
		}
		tail.toks = append(tail.toks, boundaryTok{node: node, idx: -1, prev: tail.last, t: createdAt})
		found := -1
		for i, c := range childrenOf(node) {
			if known(c.id.CreatedAt) {
				found = i
				break
			}
			tail.toks = append(tail.toks, boundaryTok{text: true, node: node, idx: i, prev: tail.last, t: c.id.CreatedAt})
		}
		if found >= 0 {
			tail.rNode, tail.rIdx, tail.rKey = node, found, boundaryKeyOf(childrenOf(node)[found])
			break
		}
		tail.last = node
	}

	tail.tokIdx = make(map[*TreeNode]int, len(tail.toks))
	for i, tk := range tail.toks {
		if tk.text {
			tail.tokIdx[childrenOf(tk.node)[tk.idx]] = i
		} else {
			tail.tokIdx[tk.node] = i
		}
	}
	return tail
}

// boundaryInsertIndex returns the index into tail.toks before which a remote
// split opens: in front of the first item whose emission belongs to an older
// sibling boundary, else in front of the first item of an outer level's
// boundary, else before R.
func (t *Tree) boundaryInsertIndex(tail *boundaryTail, anchor, nest boundaryKey, editedAt *time.Ticket) int {
	isSibling := func(m *TreeNode) bool {
		return anchorOf(m).equal(anchor) && nestOf(m).equal(nest)
	}
	for i := range tail.toks {
		owner := t.boundaryRootAt(tail, i, isSibling)
		if owner != nil && !owner.id.CreatedAt.After(editedAt) {
			return i
		}
	}

	if nest.isZero() {
		return len(tail.toks)
	}
	nestNode := t.nodeByKey(nest)
	if nestNode == nil {
		return len(tail.toks)
	}
	// The nest chain can be as long as the document (every Enter at the end
	// of the last paragraph nests under the one before), so it is bounded by
	// the nodes it visits, not by a fixed depth that would cut the outer
	// levels off.
	var levels []boundaryKey
	var visited nodeSet
	for cur := nestNode; cur != nil && visited.visit(cur); cur = t.nodeByKey(nestOf(cur)) {
		levels = append(levels, nestOf(cur))
		if nestOf(cur).isZero() {
			break
		}
	}
	outer := func(m *TreeNode) bool {
		if !anchorOf(m).equal(anchor) {
			return false
		}
		for _, l := range levels {
			if nestOf(m).equal(l) {
				return true
			}
		}
		return false
	}
	for i := range tail.toks {
		if t.boundaryRootAt(tail, i, outer) != nil {
			return i
		}
	}
	return len(tail.toks)
}

// boundaryRootAt returns the boundary whose emission tail.toks[i] is part of,
// at the level where match says a boundary counts, or nil.
func (t *Tree) boundaryRootAt(tail *boundaryTail, i int, match func(*TreeNode) bool) *TreeNode {
	// Each step moves to a boundary token; visiting one twice means the
	// records loop, so stop rather than spin.
	var visited nodeSet
	for {
		tk := tail.toks[i]
		if tk.text {
			j := i - 1
			for j >= 0 && (tail.toks[j].text || !tk.t.After(tail.toks[j].t)) {
				j--
			}
			if j < 0 {
				return nil
			}
			i = j
			continue
		}
		m := tk.node
		if !visited.visit(m) {
			return nil
		}
		if match(m) {
			return m
		}
		up := nestOf(m)
		if up.isZero() {
			up = anchorOf(m)
		}
		if up.isZero() || up.end {
			return nil
		}
		n := t.nodeByKey(up)
		if n == nil {
			return nil
		}
		k, ok := tail.tokIdx[n]
		if !ok {
			return nil
		}
		i = k
	}
}

// nodeByKey finds the node a boundary key names, or nil for the end or a node
// this replica no longer holds.
func (t *Tree) nodeByKey(key boundaryKey) *TreeNode {
	if key.id == nil {
		return nil
	}
	if id, node := t.NodeMapByID.Floor(key.id); node != nil && id.Equal(key.id) {
		return node
	}
	return nil
}

// TreeBoundary is the boundary record of one split product, as a snapshot
// carries it. A nil Anchor means the end of the split family.
type TreeBoundary struct {
	Product *TreeNodeID
	Anchor  *TreeNodeID
	Nest    *TreeNodeID
}

// Boundaries returns the boundary records of the tree's split products, in
// the post-order of index.Traverse, which is the same on every replica.
func (t *Tree) Boundaries() []TreeBoundary {
	var out []TreeBoundary
	index.Traverse(t.IndexTree, func(n *index.Node[*TreeNode], _ int) {
		if node := n.Value; hasBoundaryRecord(node) {
			out = append(out, TreeBoundary{Product: node.id, Anchor: node.BoundaryAnchor, Nest: node.BoundaryNest})
		}
	})
	return out
}

// RestoreBoundaries applies records read from a snapshot. A record naming a
// node the tree does not hold, or a text node, is ignored.
func (t *Tree) RestoreBoundaries(boundaries []TreeBoundary) {
	for _, b := range boundaries {
		product := t.nodeByKey(boundaryKey{id: b.Product})
		if product == nil || product.IsText() {
			continue
		}
		t.setBoundary(product, boundaryKey{id: b.Anchor, end: b.Anchor == nil}, boundaryKey{id: b.Nest})
	}
}

// setBoundary records anchor and nest on product and keeps anchoredBefore in
// step with it.
func (t *Tree) setBoundary(product *TreeNode, anchor, nest boundaryKey) {
	t.unindexBoundary(product)
	product.BoundaryAnchor, product.BoundaryAtEnd = anchor.id, anchor.end
	product.BoundaryNest = nest.id
	t.indexBoundary(product)
}

func (t *Tree) indexBoundary(product *TreeNode) {
	if product.BoundaryAnchor == nil {
		return
	}
	if t.anchoredBefore == nil {
		t.anchoredBefore = make(map[string]map[*TreeNode]struct{})
	}
	key := product.BoundaryAnchor.toIDString()
	set := t.anchoredBefore[key]
	if set == nil {
		set = make(map[*TreeNode]struct{})
		t.anchoredBefore[key] = set
	}
	set[product] = struct{}{}
}

func (t *Tree) unindexBoundary(product *TreeNode) {
	if product.BoundaryAnchor == nil || t.anchoredBefore == nil {
		return
	}
	key := product.BoundaryAnchor.toIDString()
	if set := t.anchoredBefore[key]; set != nil {
		delete(set, product)
		if len(set) == 0 {
			delete(t.anchoredBefore, key)
		}
	}
}

// unindexBoundaries drops the products in the subtree of node from
// anchoredBefore, as node is purged.
func (t *Tree) unindexBoundaries(node *TreeNode) {
	if len(t.anchoredBefore) == 0 || node.IsText() {
		return
	}
	index.TraverseNode(node.Index, func(n *index.Node[*TreeNode], _ int) {
		t.unindexBoundary(n.Value)
	})
}

// rebuildBoundaryIndex fills anchoredBefore from the products' records, after
// a tree is built from a snapshot or a copy.
func (t *Tree) rebuildBoundaryIndex() {
	t.anchoredBefore = nil
	index.Traverse(t.IndexTree, func(n *index.Node[*TreeNode], _ int) {
		t.indexBoundary(n.Value)
	})
}

// relinkSubtreeAnchors relinks the anchors before every node of the subtree
// that node's purge detaches, descendants first, while the subtree is still in
// the tree. GC visits its pairs in map order and can reach a child after its
// purged parent, when the detached child no longer has a next item to relink
// to; relinking the whole subtree here gives the order a child-first pass
// would, whichever order GC takes.
func (t *Tree) relinkSubtreeAnchors(node *TreeNode) {
	if len(t.anchoredBefore) > 0 && !node.IsText() {
		for _, child := range childrenOf(node) {
			t.relinkSubtreeAnchors(child)
		}
	}
	t.relinkAnchors(node)
}

// relinkAnchors moves every boundary anchored before node to the item that
// follows it in the sequence, as node is purged.
//
// Element products are relinked as well as text. One GC pass can purge a
// text node and the product after it, and it visits its pairs in map order:
// relinking only text would leave an anchor naming the purged product when
// the text went first, and the end of the family when the product went
// first. Relinking both makes the two orders meet. The purged product's own
// children go with it, so the item after it is the next boundary.
func (t *Tree) relinkAnchors(node *TreeNode) {
	if node.Index.Parent == nil {
		return
	}
	set := t.anchoredBefore[node.id.toIDString()]
	if len(set) == 0 {
		return
	}

	var walker insNextWalker
	var next boundaryKey
	if node.IsText() {
		siblings := childrenOf(node.Index.Parent.Value)
		if i := indexOfNode(siblings, node); i >= 0 && i+1 < len(siblings) {
			next = boundaryKeyOf(siblings[i+1])
		} else {
			next = boundaryKeyOf(t.nextBoundaryNode(node.Index.Parent.Value, &walker))
		}
	} else {
		walker.visit(node)
		next = boundaryKeyOf(t.nextBoundaryNode(node, &walker))
	}

	products := make([]*TreeNode, 0, len(set))
	for p := range set {
		products = append(products, p)
	}
	for _, p := range products {
		t.setBoundary(p, next, nestOf(p))
	}
}

// boundaryMergeTarget returns where a merge moves the children of src, an
// anchored split product, when the merge names target. Removing a boundary
// joins its content to the boundary before it in the sequence. If products
// the merging change did not know have opened between target and src, the
// one nearest to src is that boundary: a replica that applied the merge
// before those splits moved the content into target, and those splits then
// carried it on into their products.
func (t *Tree) boundaryMergeTarget(target, src *TreeNode, versionVector time.VersionVector) *TreeNode {
	if len(versionVector) == 0 || !hasBoundaryRecord(src) || src == target {
		return target
	}
	dest := target
	var walker insNextWalker
	walker.visit(target)
	for node := t.nextBoundaryNode(target, &walker); node != nil; node = t.nextBoundaryNode(node, &walker) {
		if node == src {
			return dest
		}
		if !node.IsRemoved() && !time.TicketKnown(versionVector, node.id.CreatedAt) {
			dest = node
		}
	}
	return target
}

// followMovedMergeChildren repoints the merge forwarding of a merge source
// whose moved children a split has carried from target into product.
//
// rebuildMergeState derives a source's mergedInto from the parent of its first
// moved child when a snapshot loads. At runtime it was fixed when the merge
// ran, so once a later split carried every child the merge moved out of
// target, a replica that reloaded forwarded inserts to product and one that
// did not forwarded them to target. Following the children here keeps the
// runtime pointer equal to the one a snapshot rebuilds.
func (t *Tree) followMovedMergeChildren(target, product *TreeNode) {
	for _, c := range childrenOf(product) {
		if c.MergedFrom == nil {
			continue
		}
		src := t.findMergeNode(c.MergedFrom)
		if src == nil || src.mergedInto == nil || !src.mergedInto.Equal(target.id) {
			continue
		}
		stays := false
		for _, k := range childrenOf(target) {
			if k.MergedFrom != nil && k.MergedFrom.Equal(src.id) {
				stays = true
				break
			}
		}
		if !stays {
			src.mergedInto = product.id
		}
	}
}

// preMergeHolder returns where a leftmost insert into a merged-away node goes
// when the first child the merge moved now opens product, a node split off
// after that merge: the end of the node before product in the family.
//
// The insert did not know the merge, so it did not know any split made after
// it either, and text stays on the left of a boundary it did not know. A
// replica that applied the insert first had it in front of the moved child
// when the later split ran, and the split left it behind. It returns nil when
// product was not split off after the merge, or has no live node before it.
//
// Only a product anchored before the moved child opened at the moved run. One
// anchored before the merged-away node, or anything else, was split by a
// change that did not know the merge and opened in front of that node: the
// merge joined the node's content, and the insert with it, to that product
// (unknownMergeBoundaryAt gives every replica that record). The ticket order
// is a necessary condition for the split to have known the merge, not a
// sufficient one, and no state the replicas share tells the two apart when
// the anchor is the moved child as well; see the design document.
func (t *Tree) preMergeHolder(product, moved *TreeNode) *TreeNode {
	if product.InsPrevID == nil || moved.MergedAt == nil || moved.MergedFrom == nil {
		return nil
	}
	if !anchorOf(product).equal(boundaryKeyOf(moved)) {
		return nil
	}
	if !product.id.CreatedAt.After(moved.MergedAt) {
		return nil
	}
	pred := t.findFloorNode(product.InsPrevID)
	if pred == nil || pred.IsText() || pred.IsRemoved() || pred.Index.Parent == nil {
		return nil
	}
	return pred
}

// lastChildOrSelf returns the last child of node, tombstones included, or node
// itself when it has none: the (parent, left) of a position at its end.
func lastChildOrSelf(node *TreeNode) *TreeNode {
	children := childrenOf(node)
	if len(children) == 0 {
		return node
	}
	return children[len(children)-1]
}

// boundaryPurgeBarrier is the barrier of a text tombstone that is the last child
// of its parent: the scan of advanceAcrossBoundaries continues from there into
// the next split product, so that product's boundary is the next item.
func (t *Tree) boundaryPurgeBarrier(node *TreeNode) (*time.Ticket, bool) {
	if !node.IsText() || node.Index.Parent == nil {
		return nil, false
	}
	if node.Index.Parent.NextSiblingOf(node.Index) != nil {
		return nil, false
	}
	var walker insNextWalker
	next := t.nextBoundaryNode(node.Index.Parent.Value, &walker)
	if next == nil {
		return nil, true
	}
	return next.id.CreatedAt, true
}
