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
// The rules cover level-0 splits of a node whose children are all text.
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
	offset int,
	editedAt *time.Ticket,
	versionVector time.VersionVector,
) (target *TreeNode, off int, anchor, nest boundaryKey, ok bool) {
	if parent.Index.Parent == nil || hasElementChild(parent, versionVector) {
		return nil, 0, boundaryKey{}, boundaryKey{}, false
	}
	local := len(versionVector) == 0
	known := func(tk *time.Ticket) bool {
		return local || time.TicketKnown(versionVector, tk)
	}

	// R is in parent: everything before it is concurrent and stays on the
	// left (§7.3).
	seen := t.seenParentOf(parent, offset, known)
	children := childrenOf(parent)
	for i := offset; i < len(children); i++ {
		if known(children[i].id.CreatedAt) {
			anchor := boundaryKeyOf(children[i])
			return parent, i, anchor, boundaryNestOf(seen, anchor, known), true
		}
	}

	tail := t.boundaryTailOf(parent, known)
	anchor = tail.rKey
	nest = boundaryNestOf(seen, anchor, known)
	if local {
		target, off = tail.at(len(tail.toks))
		return target, off, anchor, nest, true
	}
	target, off = tail.at(t.boundaryInsertIndex(tail, anchor, nest, editedAt))
	target, off = t.liveSplitTarget(tail, parent, target, off)
	return target, off, anchor, nest, true
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

// seenParentOf returns the node the editing change saw as the parent of the
// split position. A merge the change did not know may have moved the children
// around the position out of the node it split and into the merge target, as
// undoing a split does; the moved children still name that node in
// MergedFrom. Nesting is decided from there, so that the replica that applied
// the merge first records what the one that applied the split first does.
func (t *Tree) seenParentOf(parent *TreeNode, offset int, known func(*time.Ticket) bool) *TreeNode {
	children := childrenOf(parent)
	for _, i := range []int{offset - 1, offset} {
		if i < 0 || i >= len(children) {
			continue
		}
		c := children[i]
		if c.MergedFrom == nil || c.MergedAt == nil || known(c.MergedAt) {
			continue
		}
		if src := t.findMergeNode(c.MergedFrom); src != nil && src.Index.Parent != nil {
			return src
		}
	}
	return parent
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
func (t *Tree) boundaryTailOf(parent *TreeNode, known func(*time.Ticket) bool) *boundaryTail {
	tail := &boundaryTail{rKey: boundaryKey{end: true}, last: parent}
	var walker insNextWalker
	walker.visit(parent)
	for node := t.nextBoundaryNode(parent, &walker); node != nil; node = t.nextBoundaryNode(node, &walker) {
		createdAt := node.id.CreatedAt
		// A boundary the change saw removed is not in its sequence: it is
		// neither R nor a place to open a split. Its tombstone also sits
		// where its removal left it, which differs between replicas.
		gone := known(createdAt) && node.removedAt != nil && known(node.removedAt)
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
