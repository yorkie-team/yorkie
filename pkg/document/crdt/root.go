/*
 * Copyright 2020 The Yorkie Authors. All rights reserved.
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

// Package crdt provides the implementation of the CRDT data structure.
// The CRDT data structure is a data structure that can be replicated and
// shared among multiple replicas.
package crdt

import (
	"errors"
	"maps"

	"github.com/yorkie-team/yorkie/pkg/document/resource"
	"github.com/yorkie-team/yorkie/pkg/document/time"
)

// ElementPair represents pair that has a parent element and child element.
type ElementPair struct {
	parent Container
	elem   Element
}

// Elem returns the element of the ElementPair for testing purposes.
func (p *ElementPair) Elem() Element {
	return p.elem
}

// gcPairKey identifies a registered GC pair by both of its ends.
//
// The child's id alone is not unique document-wide. An RHTNode is identified
// by (updatedAt, key), and a split deep-copies the attributes of the node it
// splits -- tombstones included, because the copy has to reject the same stale
// styles the original does. The copy is therefore a distinct piece of garbage
// wearing the original's id. Keying on the child alone made the two collide,
// and since RegisterGCPair reads a second registration under a known key as an
// un-registration, the second tombstone cancelled the first instead of joining
// it.
//
// The parent is the discriminator because it is what Purge is called on: two
// pairs that share a parent and a child id name the same collectable thing,
// two that differ in either do not. It is held as the interface value rather
// than an id string because no GC parent carries an identifier today, and
// giving three of the five one (RGATreeList, RGATreeSplit and TextValue know
// nothing of their element's createdAt) costs more than the key is worth.
//
// An interface in a map key panics if its dynamic type is not comparable.
// gcParentsAreComparable checks the implementations this repo has; it cannot
// check every possible one, because GCParent is exported and satisfied
// structurally. Sealing it with an unexported method does not close that hole
// either -- a struct embedding one of these inherits the seal, so
// `struct{ *Tree; notes []string }` still satisfies the interface and still
// panics as a key. Nothing outside this module implements GCParent today, and
// every Parent this repo constructs is one of the five below.
type gcPairKey struct {
	parent GCParent
	child  string
}

// keyOf returns the map key identifying the given pair.
func keyOf(pair GCPair) gcPairKey {
	return gcPairKey{parent: pair.Parent, child: pair.Child.IDString()}
}

// comparableGCParent fails to compile if T is not usable in gcPairKey.
func comparableGCParent[T comparable](T) {}

// gcParentsAreComparable is never called; the blank assignment below only
// keeps it compiled. It exists so that making one of the
// GC parents non-comparable -- a struct with a slice, map or func field,
// passed by value -- is a build failure here rather than a runtime panic
// inside NewRoot, which the server runs on every snapshot rebuild. Add a line
// when you add a parent.
func gcParentsAreComparable() {
	comparableGCParent[*Tree](nil)
	comparableGCParent[*TreeNode](nil)
	comparableGCParent[*TextValue](nil)
	comparableGCParent[*RGATreeList](nil)
	comparableGCParent[*RGATreeSplit[*TextValue]](nil)
}

var _ = gcParentsAreComparable

// Root.collect releases a Tree's live nodes only through this assertion, so
// a Tree that stops implementing it would book purges by the plain path,
// silently; this fails the build instead.
var _ GCLiveRelease[GCChild] = (*Tree)(nil)

// Root is a structure represents the root of JSON. It has a hash table of
// all JSON elements to find a specific element when applying remote changes
// received from server.
//
// Every element has a unique time ticket at creation, which allows us to find
// a particular element.
type Root struct {
	object           *Object
	elementMap       map[string]Element
	gcElementPairMap map[string]ElementPair
	gcNodePairMap    map[gcPairKey]GCPair
	docSize          resource.DocSize

	// detachedTrees holds the Trees whose purges left detached subtrees, so
	// a collection lets them go without walking every element (see
	// GarbageCollect).
	detachedTrees map[*Tree]struct{}

	// retainedOff holds the GC size of a released node whose pair key
	// another node's pair holds, which only crafted duplicate IDs give (see
	// bookRetained); DropGCPair gives it back when the node is let go.
	retainedOff map[*TreeNode]resource.DataSize

	// sizeInGC maps every registered element whose size counts toward
	// docSize.GC rather than docSize.Live to the exact amount charged. Each
	// element's size belongs to exactly one of the two, and an element reaches
	// GC by more routes than it has removals: it can be removed itself, or be
	// a descendant of a removed container. Recording the amount rather than a
	// flag keeps the two sides symmetric even though DataSize is not stable
	// over an element's lifetime -- it grows by a ticket the moment removedAt
	// is set, which can happen after the size has already moved.
	//
	// It is keyed by the element, not by its createdAt. A createdAt is meant
	// to name one element, but it does not for the whole of a document's life:
	// undo restores `value.DeepCopy()` of a removed element, and the copy
	// keeps the original's createdAt while the original is still a tombstone.
	// Undoing an array removal reissues a ticket for the restored container
	// alone, so its members come back aliasing the tombstoned ones outright.
	// One slot per createdAt cannot describe both: whichever is charged last
	// displaces the other, and collecting the displaced one then takes a size
	// out of docSize.Live that Live was never holding -- which is how docSize
	// goes negative. One slot per element has room for both.
	//
	// A zero size is not the same as no record. It says this element has been
	// released: charged to neither side, because its subtree was orphaned by a
	// restore and nothing will ever collect it. Anything that later charges it
	// again has to know Live is not the side to take it from. Such a record
	// lives only as long as its element is addressable: index retires it when
	// a restored copy takes the element's elementMap slot over.
	sizeInGC map[Element]resource.DataSize
}

// NewRoot creates a new instance of Root.
func NewRoot(root *Object) *Root {
	r := &Root{
		elementMap:       make(map[string]Element),
		gcElementPairMap: make(map[string]ElementPair),
		gcNodePairMap:    make(map[gcPairKey]GCPair),
		detachedTrees:    make(map[*Tree]struct{}),
		retainedOff:      make(map[*TreeNode]resource.DataSize),
		sizeInGC:         make(map[Element]resource.DataSize),
		docSize: resource.DocSize{
			Live: resource.DataSize{
				Data: 0,
				Meta: 0,
			},
			GC: resource.DataSize{
				Data: 0,
				Meta: 0,
			},
		},
	}

	r.object = root

	// NOTE(hackerwins): tombstoned elements are not re-registered here:
	// RegisterElement books every one of them into GC, along with the
	// tombstones its elements carry inside themselves.
	r.RegisterElement(root, nil)

	return r
}

// Object returns the root object of the JSON.
func (r *Root) Object() *Object {
	return r.object
}

// FindByCreatedAt returns the element of given creation time.
func (r *Root) FindByCreatedAt(createdAt *time.Ticket) Element {
	return r.elementMap[createdAt.Key()]
}

// RegisterElement registers the given element and its descendants to hash
// table.
func (r *Root) RegisterElement(element Element, parent Container) {
	r.registerLive(element)

	// NOTE(hackerwins): An element can be registered while it already carries a
	// removedAt. An undo re-sets the DeepCopy its reverse captured, and that
	// copy keeps the members that were tombstoned before the container was;
	// NewRoot loads a document that still holds tombstones; and the losing side
	// of an LWW Set is marked removed by ElementRHT before it is booked. The
	// size registered above is the post-removal one in every such case, so it
	// belongs to GC rather than Live, and the tombstone has to be collectable.
	// Booking it here is what makes both true whichever route brought it in,
	// and it is the one place all of them pass through.
	//
	// This is a second pass on purpose. Adopting a tombstone moves its whole
	// subtree, so doing it while the first pass is still walking would move
	// descendants Live has not been charged for yet, and drive Live negative.
	r.adoptTombstones(element, parent)

	// The two passes above see elements only. A Text/Tree/Array also holds
	// tombstones *inside* itself -- removed tree nodes, removed text pieces,
	// removed RHT attributes -- and those are collected through gcNodePairMap,
	// which only a GC pair reaches.
	//
	// Every route that brings such an element in has to book them, not just
	// snapshot load: a Set/Add/ArraySet payload is decoded by the same
	// BytesTo* readers a snapshot is, and an undo re-sets a DeepCopy of a
	// removed container whose tree may still hold nodes that were tombstoned
	// before it. Left unbooked they are the worst of both states -- invisible
	// to every later edit, yet charged to nothing and collectable by nothing,
	// so the bytes stay in the document forever. A crafted payload whose tree
	// nodes all arrive with removedAt reaches exactly that state on purpose.
	r.registerInternalGCPairs(element)
}

// registerInternalGCPairs books the tombstones the given element and its
// descendant elements carry inside themselves into GC.
//
// Freshly created content has none, so this costs one walk of the registered
// subtree and no registration in the common case.
func (r *Root) registerInternalGCPairs(element Element) {
	register := func(elem Element) {
		switch e := elem.(type) {
		case *Array:
			for _, pair := range e.GCPairs() {
				r.RegisterGCPair(pair)
			}
		case *Text:
			for _, pair := range e.GCPairs() {
				r.RegisterGCPair(pair)
			}
		case *Tree:
			for _, pair := range e.GCPairs() {
				r.RegisterGCPair(pair)
			}
		}
	}

	register(element)

	if container, ok := element.(Container); ok {
		container.Descendants(func(elem Element, _ Container) bool {
			register(elem)
			return false
		})
	}
}

// registerLive registers the given element and its descendants to the element
// map, and charges docSize.Live for each.
func (r *Root) registerLive(element Element) {
	r.index(element)
	r.docSize.Live.Add(element.DataSize())

	if container, ok := element.(Container); ok {
		container.Descendants(func(elem Element, _ Container) bool {
			r.index(elem)
			r.docSize.Live.Add(elem.DataSize())
			return false
		})
	}
}

// index points elementMap at the given element, and retires the release
// record of the element whose slot it takes over.
//
// A release record guards a tombstone's subtree only while it can still be
// addressed (see release), and elementMap is what addresses an element. The
// restore that orphans a subtree re-registers a copy under the same
// createdAts right after releasing it, so the copy's elements take those
// slots over here, and from then on nothing can reach the released ones:
// operations resolve their target through elementMap, and release has
// already dropped their collection entries. Nothing else ever deletes such
// a record -- deregisterElement never runs for an orphan -- and the map is
// keyed by the element, so leaving it would keep a record and a dead subtree
// alive per remove/undo cycle. A member only the tombstone carries keeps its
// slot and its record.
//
// Only a zero record is a release record. A tombstone with a charge of its
// own is still on the collection worklist, and collecting it is what
// retires that record.
func (r *Root) index(elem Element) {
	key := elem.CreatedAt().Key()
	if prev, ok := r.elementMap[key]; ok && prev != elem {
		if charged, ok := r.sizeInGC[prev]; ok && charged == (resource.DataSize{}) {
			delete(r.sizeInGC, prev)
			// A released Tree kept its detached subtrees only while a
			// change could still name them through it (see release); with
			// its slot taken over, nothing can, so they go now, with the
			// pairs and the retained bytes their nodes hold, and so do the
			// pairs of its attached tombstones, which nothing would
			// otherwise collect before a pass at a vector covering them.
			if tree, isTree := prev.(*Tree); isTree {
				r.dropNodes(tree, tree.dropSubtrees(tree.tops()))
				r.dropNodes(tree, subtreeOf(tree.Root()))
				delete(r.detachedTrees, tree)
			}
		}
	}
	r.elementMap[key] = elem
}

// adoptTombstones books every element of the given subtree that already carries
// a removedAt into GC.
//
// The subtree's own root is skipped when it has no parent. That is the document
// root, which NewRoot registers with a nil parent; a snapshot can decode one
// that carries a removedAt, and booking it would leave collect with a nil
// parent to purge it from. Leaving it charged to Live is the safe reading: a
// root nothing can purge is not garbage.
func (r *Root) adoptTombstones(element Element, parent Container) {
	if element.RemovedAt() != nil && parent != nil {
		r.adoptRemovedElementPair(parent, element)
	}

	container, ok := element.(Container)
	if !ok {
		return
	}

	container.Descendants(func(elem Element, par Container) bool {
		if elem.RemovedAt() != nil {
			r.adoptRemovedElementPair(par, elem)
		}
		return false
	})
}

// adoptRemovedElementPair books an element that was already tombstoned when it
// was registered, and its descendants, into GC. It is
// RegisterRemovedElementPair without the ticket refund: the size just charged
// to Live already included the removedAt ticket, so Live has nothing to get
// back.
func (r *Root) adoptRemovedElementPair(parent Container, elem Element) {
	r.moveSizeToGC(elem)
	r.moveDescendantsToGC(elem)
	r.gcElementPairMap[elem.CreatedAt().Key()] = ElementPair{parent, elem}
}

// moveDescendantsToGC moves the size of every descendant of the given element
// from Live to GC.
//
// NOTE(hackerwins): RegisterElement books a container and every descendant into
// Live, and deregisterElement subtracts both when the tombstone is collected.
// Removing a container therefore has to move its descendants as well: booking
// only the container itself would strand their size in Live forever and drive
// GC negative once the collection subtracted them.
func (r *Root) moveDescendantsToGC(elem Element) {
	container, ok := elem.(Container)
	if !ok {
		return
	}

	container.Descendants(func(e Element, _ Container) bool {
		r.moveSizeToGC(e)
		return false
	})
}

// deregisterElement deregister the given element from hash tables.
func (r *Root) deregisterElement(element Element) int {
	count := 0

	deregister := func(elem Element) {
		createdAt := elem.CreatedAt().Key()
		if tree, ok := elem.(*Tree); ok {
			// Its detached subtrees go with it, and the pairs their nodes
			// hold: collect leaves a released node's to the drop, which a
			// tree out of the document no longer gets.
			r.dropNodes(tree, tree.dropSubtrees(tree.tops()))
			delete(r.detachedTrees, tree)
			tree.collected = true
		}
		// Subtract the size from wherever it is actually counted, and by the
		// amount actually charged. A descendant created inside an
		// already-removed container never passed through a removal, so it
		// still sits in Live; subtracting it from GC would push GC below zero
		// and leave its cost in Live forever.
		if charged, ok := r.sizeInGC[elem]; ok {
			r.docSize.GC.Sub(charged)
			delete(r.sizeInGC, elem)
		} else {
			r.docSize.Live.Sub(elem.DataSize())
		}

		// NOTE(hackerwins): Drop the index entries by identity, not by key.
		// A createdAt is meant to name one element, but undo breaks that: it
		// restores `value.DeepCopy()` of a removed container, and the copy
		// keeps every descendant's createdAt while the original is still a
		// tombstone. Only the top level of an undone ArraySet gets a fresh
		// ticket (document.go's executeUndoRedo), so the descendants below it
		// are answered by the live copy while the tombstone is still the one
		// being collected here. `delete(map, createdAt)` would evict the live
		// element's entry, and every later operation addressed at it fails
		// with ErrNotApplicableDataType -- on the server's replay too, which
		// makes the stored change log unreplayable.
		//
		// Comparing the slot against the element being deregistered leaves
		// the entry alone when it has been taken over. The tombstone loses
		// nothing by it: it is already unlinked from the tree, and whatever
		// now owns the slot will clear it when its own turn comes.
		//
		// The JS SDK reads the same rule off a different structure. Its
		// gcElementSetByCreatedAt holds keys alone, because its pair map
		// already carries a parent for every element, so it takes the identity
		// from that map rather than from an entry of its own. Same rule, one
		// resolver each -- the two differ in where the identity is read, not
		// in what it decides.
		if r.elementMap[createdAt] == elem {
			delete(r.elementMap, createdAt)
		}
		if pair, ok := r.gcElementPairMap[createdAt]; ok && pair.elem == elem {
			delete(r.gcElementPairMap, createdAt)
		}
		count++
	}

	deregister(element)

	if element, ok := element.(Container); ok {
		element.Descendants(func(elem Element, parent Container) bool {
			deregister(elem)
			return false
		})
	}

	return count
}

// RegisterRemovedElementPair register the given element pair to hash table.
func (r *Root) RegisterRemovedElementPair(parent Container, elem Element) {
	moved := r.moveSizeToGC(elem)
	r.moveDescendantsToGC(elem)

	// NOTE(hackerwins): When an element is removed, parent sets the removedAt
	// to mark the child as removed. That ticket is part of the size charged to
	// GC just now, but it was not part of what Live held -- RegisterElement ran
	// before the removal -- so Live gets it back. Only on the move that carried
	// it: a size already in GC, or one moved as a descendant while its own
	// removedAt is still unset, did not.
	//
	// An element that already carried its removedAt when it was registered does
	// not get the refund. RegisterElement books it with adoptRemovedElementPair
	// and the top-up branch of moveSizeToGC then reports that nothing moved, so
	// a later removal of it lands here with moved false. Live did hold that
	// ticket, and there is nothing to give back.
	if moved && elem.RemovedAt() != nil {
		r.docSize.Live.Meta += time.TicketSize
	}

	r.gcElementPairMap[elem.CreatedAt().Key()] = ElementPair{
		parent,
		elem,
	}
}

// UnregisterRemovedElementPair drops the collection entry registered under the
// given createdAt, if there is one, and releases the GC charge it holds. It
// reports whether an entry was dropped.
//
// It is the narrow counterpart of RegisterRemovedElementPair, for the one
// caller that has to retire a tombstone without collecting it: a Set that
// restores an element under a createdAt a tombstone already answers to
// (set_operation.ts:98-104 in the JS SDK). ElementRHT.SetWithExecutedAt has by
// then re-pointed nodeMapByCreatedAt at the restored copy, so the entry the
// removal left behind now resolves, through that index, to live data -- and
// collection would purge it. Dropping the entry is what stops that.
//
// Deliberately narrow. The obvious alternative, deregistering the tombstone
// and its descendants outright, reaches past the entry that is stale: the
// tombstone's descendant set can be a strict superset of the restored copy's
// -- a peer may have added a child into the container after the undoing
// replica took its copy -- and deregistering evicts those descendants from
// elementMap with nothing to put them back. A later change addressed at one of
// them then fails on every replica, and permanently on the server, which
// replays the same change log to rebuild the document and its snapshots.
//
// What it still does reach is the accounting, and it has to. SetWithExecutedAt
// has displaced this tombstone from its container's nodeMapByCreatedAt, so the
// subtree is orphaned: dropping the entry is dropping the only thing that would
// ever have collected it. Its cost is therefore released from wherever it sits
// -- docSize.GC for anything a removal moved there, docSize.Live for a member a
// peer added into the container after it was already removed -- which is
// exactly the split deregisterElement makes on the collection path, and leaves
// the restored copy that RegisterElement is about to charge to Live as the only
// thing accounted for.
//
// The element collection entries of the subtree's own removed members go with
// it, for the same reason: nothing can reach them to collect them, so leaving
// them would hold GarbageLen above zero forever and let a later pass subtract
// a cost this call has already released. A released Tree keeps its
// detached subtrees, which a change can still name, until a collection's
// coverage lets them go (see release), and the pairs of its attached
// tombstones, which hold their own share of GC, until a collection covering
// them purges them; once a restored copy takes its slot and nothing can name
// it, both go at once (see index).
func (r *Root) UnregisterRemovedElementPair(createdAt *time.Ticket) bool {
	pair, ok := r.gcElementPairMap[createdAt.Key()]
	if !ok {
		return false
	}

	r.release(pair.elem)
	if container, ok := pair.elem.(Container); ok {
		container.Descendants(func(e Element, _ Container) bool {
			r.release(e)
			return false
		})
	}

	delete(r.gcElementPairMap, createdAt.Key())
	return true
}

// release forgets the cost of an element that has become unreachable without
// being collected, and any collection entry naming it. It leaves elementMap
// alone: the slot may since have been taken over by a live element restored
// under this same createdAt, and that element's registration has to stand.
func (r *Root) release(elem Element) {
	createdAt := elem.CreatedAt().Key()

	// Subtract from whichever side is actually holding it, by the amount
	// actually charged -- the same split deregisterElement makes. A member
	// added into an already-removed container never passed through a removal,
	// so it still sits in Live.
	if charged, ok := r.sizeInGC[elem]; ok {
		r.docSize.GC.Sub(charged)
	} else {
		r.docSize.Live.Sub(elem.DataSize())
	}

	// Record the release rather than forgetting it. This element stays
	// addressable -- that is the whole point of not deregistering it -- so a
	// peer that has not seen the restore can still remove something inside
	// this subtree, and moveSizeToGC would then take its size out of Live for
	// a second time and drive docSize negative. A zero charge says Live is
	// not holding it. A copy restored under the same createdAt has a slot of
	// its own and is still charged normally. Registering that copy is also
	// what retires this record, once the copy takes the element's elementMap
	// slot over and nothing can address it any more (see index).
	r.sizeInGC[elem] = resource.DataSize{}

	if pair, ok := r.gcElementPairMap[createdAt]; ok && pair.elem == elem {
		delete(r.gcElementPairMap, createdAt)
	}

	// A released Tree's charge is gone, so what a later release or edit
	// inside it moves is booked to neither side (see bookRelease,
	// ChargeToRemovedElement). It stays addressable, unlike a deregistered
	// one, so a change that has not seen a node of its detached subtrees can
	// still name it: they stay, with the pairs their nodes hold, until a
	// collection's coverage lets them go, as for any other (dropDetached).
	if tree, ok := elem.(*Tree); ok {
		tree.collected = true
	}
}

// moveSizeToGC moves the size of the given element from Live to GC, and
// reports whether it moved a size Live was holding. A size already charged to
// GC for this same element -- because it was removed before, because a
// container above it was, or because a restore released it -- only has its
// charge topped up: DataSize grows by a ticket when removedAt is set, which
// can happen after the move.
//
// Another element sharing this createdAt has a charge of its own, and it is not
// this one's: this element is still in Live and moves in full.
func (r *Root) moveSizeToGC(elem Element) bool {
	size := elem.DataSize()

	if charged, ok := r.sizeInGC[elem]; ok {
		diff := size
		diff.Sub(charged)
		r.docSize.GC.Add(diff)
		r.sizeInGC[elem] = size
		return false
	}

	r.docSize.GC.Add(size)
	r.docSize.Live.Sub(size)
	r.sizeInGC[elem] = size
	return true
}

// DocSize returns the size of the document.
func (r *Root) DocSize() resource.DocSize {
	return r.docSize
}

// DeepCopy copies itself deeply.
//
// The copy is the document's clone, which a change runs on before the root,
// so it also carries the subtrees each Tree's purges took out (see
// Tree.copyDetachedFrom): an op the root resolves through a detached node
// resolves on the clone alike. Each Tree's pairs are the root's own, booked
// for the copies of their nodes (see copyTreePairs). A Tree whose copy does
// not match it in shape, which no honest document produces, fails the copy
// with ErrTreeCopyShape.
func (r *Root) DeepCopy() (*Root, error) {
	copiedObject, err := r.object.DeepCopy()
	if err != nil {
		return nil, err
	}
	copied := NewRoot(copiedObject.(*Object))
	all := make(map[*Tree]*Tree)
	trees := make(map[*Tree]*Tree)
	copies := make(map[*TreeNode]*TreeNode)
	// Each Tree is paired with its copy by its place in the object, the two
	// walked in lockstep, not through elementMap or by createdAt alone:
	// elementMap can hold a Tree the object no longer reaches, a member a
	// peer added to a container this replica removed and an undo then
	// replaced, and a key can be shared, with another Tree or another kind
	// of element. A Tree that does not pair keeps the pairs NewRoot's scan
	// books for its copy and no detached state, as main's copy does.
	for tree, copiedTree := range pairedTrees(r.object, copied.object) {
		all[tree] = copiedTree
		// A Tree with no detached state has nothing the scan misses but a
		// pair whose node left the tree, which copyTreePairs books on its
		// own; it is not paired node by node, the common case and main's.
		if tree.detachedByID.Len() == 0 && len(tree.detachedTops) == 0 {
			continue
		}
		treeCopies, err := copiedTree.copyDetachedFrom(tree)
		if errors.Is(err, ErrTreeCopyShape) {
			// Only a broken invariant gets here. The copy keeps the scan's
			// pairs and no detached state, so a late change naming a node
			// in a detached subtree fails on it rather than every copy.
			continue
		}
		if err != nil {
			return nil, err
		}
		trees[tree] = copiedTree
		maps.Copy(copies, treeCopies)
		if _, ok := r.detachedTrees[tree]; ok {
			copied.detachedTrees[copiedTree] = struct{}{}
		}
	}
	copied.copyTreePairs(r, all, trees, copies)
	for node, size := range r.retainedOff {
		if c := copies[node]; c != nil {
			copied.retainedOff[c] = size
			copied.docSize.GC.Add(size)
		}
	}
	return copied, nil
}

// pairedTrees pairs each Tree the source object reaches with its copy in
// copied, a copy of it, by place: an object's members by their member key
// and createdAt, the n-th of several alike with the n-th, an array's
// elements by their order in the list. A createdAt can be shared, with
// another Tree or another kind of element, so it alone does not pair.
func pairedTrees(source, copied *Object) map[*Tree]*Tree {
	pairs := make(map[*Tree]*Tree)
	pairElements(source, copied, pairs)
	return pairs
}

// pairElements pairs from with to, its copy, and what they hold.
func pairElements(from, to Element, pairs map[*Tree]*Tree) {
	switch f := from.(type) {
	case *Tree:
		if t, ok := to.(*Tree); ok {
			pairs[f] = t
		}
	case *Object:
		t, ok := to.(*Object)
		if !ok {
			return
		}
		type place struct{ key, createdAt string }
		slots := make(map[place][]Element)
		for _, node := range t.memberNodes.Nodes() {
			at := place{node.Key(), node.Element().CreatedAt().Key()}
			slots[at] = append(slots[at], node.Element())
		}
		for _, node := range f.memberNodes.Nodes() {
			at := place{node.Key(), node.Element().CreatedAt().Key()}
			if len(slots[at]) == 0 {
				continue
			}
			pairElements(node.Element(), slots[at][0], pairs)
			slots[at] = slots[at][1:]
		}
	case *Array:
		t, ok := to.(*Array)
		if !ok {
			return
		}
		fromNodes, toNodes := f.elements.Nodes(), t.elements.Nodes()
		if len(fromNodes) != len(toNodes) {
			return
		}
		for i, node := range fromNodes {
			if node.Element().CreatedAt().Compare(toNodes[i].Element().CreatedAt()) == 0 {
				pairElements(node.Element(), toNodes[i].Element(), pairs)
			}
		}
	}
}

// dropScanPairs takes out the pairs NewRoot's scan booked for the Trees
// copyTreePairs pairs node by node, and for their nodes' attributes, with
// their share of GC: those Trees book the source's own.
func (r *Root) dropScanPairs(trees map[*Tree]*Tree, copies map[*TreeNode]*TreeNode) {
	if len(trees) == 0 {
		return
	}
	copiedTrees := make(map[GCParent]struct{}, len(trees))
	for _, c := range trees {
		copiedTrees[c] = struct{}{}
	}
	copiedNodes := make(map[*TreeNode]struct{}, len(copies))
	for _, c := range copies {
		copiedNodes[c] = struct{}{}
	}
	for key, pair := range r.gcNodePairMap {
		_, owned := copiedTrees[pair.Parent]
		if node, ok := pair.Parent.(*TreeNode); ok {
			_, owned = copiedNodes[node]
		}
		if !owned {
			continue
		}
		if pair.GCOnlySize != nil {
			r.docSize.GC.Sub(*pair.GCOnlySize)
		} else {
			r.docSize.GC.Sub(pair.Child.DataSize())
		}
		delete(r.gcNodePairMap, key)
	}
}

// bookCopiedPair books a pair copyTreePairs carries over, the child's size
// to GC. A pair the scan booked under the key, for a node of the copied
// tree that shares the ID of one out of it, gives its share back: the root
// holds one pair per key, this one.
func (r *Root) bookCopiedPair(parent GCParent, child GCChild) {
	size := child.DataSize()
	pair := GCPair{Parent: parent, Child: child, GCOnlySize: &size}
	key := keyOf(pair)
	if prev, ok := r.gcNodePairMap[key]; ok {
		if prev.GCOnlySize != nil {
			r.docSize.GC.Sub(*prev.GCOnlySize)
		} else {
			r.docSize.GC.Sub(prev.Child.DataSize())
		}
	}
	r.gcNodePairMap[key] = pair
	r.docSize.GC.Add(size)
}

// copyTreePairs replaces the pairs NewRoot's scan booked for the copied
// Trees with the ones src holds for their sources, booked for the copies of
// their nodes. The scan reaches the attached tree only and books through
// RegisterGCPair, whose second registration under a key cancels the first,
// so two nodes sharing an ID would leave the copy with neither; src's own
// entries are what it collects, so the copy collects the same. A pair whose
// node has no copy, one no longer in its tree, is booked for a copy of the
// node alone, which collect purges as it does the original.
//
// all maps each reachable source Tree to its copy; trees, those with
// detached state, which were paired node by node, and copies each of their
// nodes to its copy, so one pass over each pair map does it all. A Tree with
// no detached state keeps the scan's pairs.
func (r *Root) copyTreePairs(src *Root, all, trees map[*Tree]*Tree, copies map[*TreeNode]*TreeNode) {
	r.dropScanPairs(trees, copies)
	book := r.bookCopiedPair
	// copyOf answers the copy of a node, copying one no longer in its tree
	// on its own. Only a node out of every tree has none: an attached one
	// was copied with its tree.
	copyOf := func(node *TreeNode) *TreeNode {
		if c := copies[node]; c != nil {
			return c
		}
		if !node.detached && node.Index != nil && node.Index.Parent != nil {
			return nil
		}
		c, err := node.DeepCopy()
		if err != nil {
			return nil
		}
		copies[node] = c
		return c
	}
	attrsOf := make(map[*TreeNode]map[string]*RHTNode)
	for _, pair := range src.gcNodePairMap {
		if tree, ok := pair.Parent.(*Tree); ok {
			node, isNode := pair.Child.(*TreeNode)
			if !isNode {
				continue
			}
			copiedTree, paired := trees[tree]
			if !paired {
				// A Tree the scan alone books. Defensive: a tree without
				// detached state holds no pair for a node out of it, as a
				// purge deletes the pair of the node it takes out and
				// detaches nothing else; were one there, the scan would not
				// reach it, so it is copied on its own.
				attached := !node.detached && node.Index != nil && node.Index.Parent != nil
				if copiedTree, ok = all[tree]; !ok || attached {
					continue
				}
			}
			if c := copyOf(node); c != nil {
				book(copiedTree, c)
			}
			continue
		}
		node, ok := pair.Parent.(*TreeNode)
		if !ok {
			continue
		}
		if _, ours := copies[node]; !ours && !node.detached {
			// An attached node of a Tree not in trees: the scan booked it.
			continue
		}
		c := copyOf(node)
		if c == nil || c.Attrs == nil {
			continue
		}
		byID, ok := attrsOf[c]
		if !ok {
			byID = make(map[string]*RHTNode)
			for _, attr := range c.Attrs.Nodes() {
				byID[attr.IDString()] = attr
			}
			attrsOf[c] = byID
		}
		if attr := byID[pair.Child.IDString()]; attr != nil {
			book(c, attr)
		}
	}
}

// Rederive returns a copy of the root whose ledger is derived from the
// copy's own content, as an oracle for the running one: NewRoot's scan of
// the attached trees, and for the detached subtrees each Tree carries,
// walked from their tops, the pending pair of every node that is a
// tombstone, released ones included, and those of their removed
// attributes. DeepCopy takes the root's own pairs
// instead, so it cannot tell a ledger that drifted from one that did not.
// For tests and diagnostics, as NodeLen is; exported for the tests of
// package document, which check a document's ledger with it.
func (r *Root) Rederive() (*Root, error) {
	copiedObject, err := r.object.DeepCopy()
	if err != nil {
		return nil, err
	}
	copied := NewRoot(copiedObject.(*Object))
	for tree, copiedTree := range pairedTrees(r.object, copied.object) {
		if _, err := copiedTree.copyDetachedFrom(tree); errors.Is(err, ErrTreeCopyShape) {
			continue
		} else if err != nil {
			return nil, err
		}
		// Walked from the tops, as the drop is, not from detachedByID, which
		// does not index a node another holds the ID of. A top's own pair
		// was purged with it; the rest of its subtree, but for nodes a
		// restore forgot, holds a pair each, booked as bookRetained does
		// one, and those of their removed attributes.
		for _, top := range copiedTree.tops() {
			for _, node := range subtreeOf(top) {
				if node.forgotten {
					continue
				}
				if node != top && node.removedAt != nil {
					copied.bookRetained(GCPair{Parent: copiedTree, Child: node})
				}
				for _, pair := range node.GCPairs() {
					copied.RegisterGCPair(pair)
				}
			}
		}
		if len(copiedTree.detachedTops) > 0 {
			copied.detachedTrees[copiedTree] = struct{}{}
		}
	}
	return copied, nil
}

// GarbageCollect purge elements that were removed before the given time.
//
// A pass can hold a purge back (see GCBarrier), and holding one back can be the
// only reason another is held back: purging a node hands its successor to the
// node in front of it, and that successor is one this pass already found
// stable. So a pass that both purged and deferred may have more to do, and the
// loop repeats until a pass purges nothing new or defers nothing. Everything
// held back stays on the worklist for the next vector that covers it.
//
// The repeat also makes the result independent of Go's map iteration order,
// which decides only how many passes it takes, not what ends up collected.
//
// A detached subtree is let go once vector has seen every node in it created
// and merged in (see dropDetached). clock is what the replica's document has
// applied; a ticket it cannot have applied is forged and counts as seen,
// since no vector would ever cover it (see DocumentClock). A nil clock
// trusts every ticket, so a forged one keeps its subtree, and the subtree's
// bytes in GC, for good: only a caller with no document to read one from,
// a test of the collection itself, passes nil. The root and its clone are
// handed the same clock.
func (r *Root) GarbageCollect(vector time.VersionVector, clock *DocumentClock) (int, error) {
	count := r.dropDetached(vector, clock)
	for {
		purged, deferred, err := r.collect(vector)
		if err != nil {
			// What the drop let go is gone already; say so.
			return count, err
		}
		count += purged

		if purged == 0 || deferred == 0 {
			break
		}
	}
	// And after: a subtree this collection took out that its vector has
	// already seen whole goes now, so one call leaves no garbage the next
	// at the same vector would collect.
	return count + r.dropDetached(vector, clock), nil
}

// DocumentClock is what a replica's document has applied: its version
// vector and its lamport, which every change it applies raises to its own
// at least. A ticket is forged when the vector does not know it and its
// lamport is above the document's: no change the document applied carries
// one, so no node it holds was made by one. The lamport bound is what keeps
// an honest ticket from looking forged when the vector lacks its actor's
// entry, which a snapshot stored with an emptied vector (an opt-out-only
// document, see disable-gc-on-attach.md) and the changes applied after it
// leave: the snapshot's lamport, and each change's, still bound every
// ticket in it. An empty vector knows every ticket.
type DocumentClock struct {
	Vector  time.VersionVector
	Lamport int64
}

// forged reports whether ticket is one no change clock has applied carried.
func (c *DocumentClock) forged(ticket *time.Ticket) bool {
	return c != nil && !time.TicketKnown(c.Vector, ticket) && ticket.Lamport() > c.Lamport
}

// dropDetached lets go of the detached subtrees vector has seen whole, in
// every Tree, with the pairs their nodes still hold (see Tree.dropDetached).
// It runs before the collection, for subtrees earlier ones took out that
// the vector has come to cover, and after it, for those it took out that
// the vector covers already. It reports how many pairs it dropped.
//
// A purge at vector V comes after every change concurrent with the removal
// it collects, by the protocol, but a change that is not concurrent with
// the removal can still name a node a concurrent split carried into the
// subtree, or a merge moved there, as long as it has not seen that split or
// merge. So a subtree is kept until a collection whose vector covers the
// creation and merge of every node in it; a change naming it after that
// fails to resolve, as on a replica that keeps no detached nodes. The rule
// reads the subtree and the vector alone, so the root and its clone, which
// collect at the same vectors, let go alike.
func (r *Root) dropDetached(vector time.VersionVector, clock *DocumentClock) int {
	count := 0
	for tree := range r.detachedTrees {
		count += r.dropNodes(tree, tree.dropDetached(vector, clock))
		if len(tree.detachedTops) == 0 {
			delete(r.detachedTrees, tree)
		}
	}
	return count
}

// bookRelease books what a purge in a Tree released (see GCLiveRelease). The
// released bytes leave whichever ledger holds them: Live, or, for a Tree
// element that is itself removed, the GC charge its removal moved them to
// (sizeInGC), and each released node's pair holds its tombstone in GC from
// then on. A Tree the collection has already deregistered took its charge,
// those bytes included, with it, so nothing is booked and the tree is not
// tracked again; the pending pairs of its nodes are collected as they come
// due, as on main. Taking the bytes from the element's charge is exact
// because whatever an operation makes live inside a removed Tree element is
// charged there too, not to Live (ChargeToRemovedElement, yorkie#2251).
func (r *Root) bookRelease(parent GCParent, result purgeResult) {
	tree, isTree := parent.(*Tree)
	if isTree && tree.collected {
		// A released tree a change can still name keeps what the purge
		// detached for the coverage drop, though it books nothing.
		if len(tree.detachedTops) > 0 && r.elementMap[tree.CreatedAt().Key()] == tree {
			r.detachedTrees[tree] = struct{}{}
		}
		return
	}
	if charged, inGC := r.sizeInGC[tree]; isTree && inGC {
		charged.Sub(result.Released)
		r.sizeInGC[tree] = charged
		r.docSize.GC.Sub(result.Released)
	} else {
		r.docSize.Live.Sub(result.Released)
	}
	for _, node := range result.ReleasedNodes {
		r.bookRetained(GCPair{Parent: parent, Child: node})
	}
	if isTree && len(tree.detachedTops) > 0 {
		r.detachedTrees[tree] = struct{}{}
	}
}

// bookRetained books the pair of a node a purge released, its tombstone's
// size to GC alone: the purge has taken its live bytes off Live, so the
// booking has to land. Inserted, not registered, so a key another node
// holds is not cancelled; when one does, which only crafted duplicate IDs
// reach, the size is held in retainedOff instead, for DropGCPair to give
// back when the node is let go.
func (r *Root) bookRetained(pair GCPair) {
	size := pair.Child.DataSize()
	r.docSize.GC.Add(size)
	key := keyOf(pair)
	if _, ok := r.gcNodePairMap[key]; ok {
		node := pair.Child.(*TreeNode)
		r.retainedOff[node] = size
		return
	}
	pair.GCOnlySize = &size
	r.gcNodePairMap[key] = pair
}

// dropNodes drops the pairs the nodes a Tree let go still hold, their own
// and those of their removed attributes, and reports how many it dropped.
func (r *Root) dropNodes(tree *Tree, nodes []*TreeNode) int {
	count := 0
	for _, node := range nodes {
		if r.DropGCPair(GCPair{Parent: tree, Child: node}) {
			count++
		}
		for _, pair := range node.GCPairs() {
			if r.DropGCPair(pair) {
				count++
			}
		}
	}
	return count
}

// collect runs one collection pass, reporting how much it purged and how much
// it held back on a barrier.
func (r *Root) collect(vector time.VersionVector) (int, int, error) {
	count, deferred := 0, 0

	for _, pair := range r.gcElementPairMap {
		// A registered pair is a claim that its element is a tombstone, and
		// both steps below trust it: EqualToOrAfter dereferences the ticket
		// without checking it, and Purge deletes whatever the element's
		// createdAt currently resolves to. An element that is registered but
		// not removed would panic on the first and delete a live member on
		// the second, which is the shape of every bug in this area.
		//
		// Nothing produces that state today -- removedAt is only ever cleared
		// on Text and Tree nodes, which live in gcNodePairMap and have their
		// own UnregisterGCPair path, never on an Element. Two known routes
		// would reach it: identity-preserving revive for Elements, deferred
		// out of this release, and RGATreeList.DeleteByCreatedAt, which hands
		// Remove.Execute a node to register even when entry.elem.Remove
		// declined (only when the delete ticket does not follow the element's
		// createdAt, which a causal change log cannot produce).
		//
		// Skipping leaves the entry on the worklist rather than dropping it,
		// so a revived element's size stays charged to GC until revive brings
		// the GC->Live accounting that gcNodePairMap already has. A leak is
		// the safe failure here; losing the element, or panicking inside a
		// server-side snapshot build, is not.
		if pair.elem.RemovedAt() == nil {
			continue
		}

		if !vector.EqualToOrAfter(pair.elem.RemovedAt()) {
			continue
		}

		// A tombstone is not only a value that is gone, it is also a place in
		// its parent that other replicas may still be deciding against.
		// removedAt covers the value; GCBarrier covers the place.
		if parent, ok := pair.parent.(GCBarrier[Element]); ok {
			if at := parent.PurgeBarrierAt(pair.elem); at != nil && !vector.EqualToOrAfter(at) {
				deferred++
				continue
			}
		}

		if err := pair.parent.Purge(pair.elem); err != nil {
			return 0, 0, err
		}

		count += r.deregisterElement(pair.elem)
	}

	// Delete by the range key, not by one recomputed after Purge: keyOf reads
	// the child's IDString, and an entry keyed on a value the purge had
	// changed would survive the delete, keep GarbageLen from ever reaching
	// zero, and have its size subtracted again on the next pass. No IDString
	// is purge-dependent today; using the key already in hand means none has
	// to stay that way.
	for key, pair := range r.gcNodePairMap {
		// A released node's pair holds its bytes until its subtree is let
		// go, not until its removal is covered (see TreeNode.released).
		if node, ok := pair.Child.(*TreeNode); ok && node.released {
			continue
		}

		// A pair whose child is not removed claims a tombstone that is not
		// there, as for an element above; it is left on the worklist rather
		// than dereferenced or purged.
		if removedAt := pair.Child.RemovedAt(); removedAt == nil || !vector.EqualToOrAfter(removedAt) {
			continue
		}

		if parent, ok := pair.Parent.(GCBarrier[GCChild]); ok {
			if at := parent.PurgeBarrierAt(pair.Child); at != nil && !vector.EqualToOrAfter(at) {
				deferred++
				continue
			}
		}

		// Live nodes under the child go with it, off Live (see
		// GCLiveRelease), so the purge detaches tombstones only. Only a Tree
		// purges a subtree; every other GC parent purges a leaf -- a text or
		// list node, an attribute -- with nothing under it to release.
		if parent, ok := pair.Parent.(GCLiveRelease[GCChild]); ok {
			result, err := parent.purgeChild(pair.Child)
			if err != nil {
				return 0, 0, err
			}
			// Defensive; unreachable because the check above skips a pair
			// whose child has no removal, the only purge that does not happen.
			if !result.Purged {
				continue
			}
			r.bookRelease(pair.Parent, result)
		} else if err := pair.Parent.Purge(pair.Child); err != nil {
			return 0, 0, err
		}

		r.docSize.GC.Sub(pair.Child.DataSize())
		delete(r.gcNodePairMap, key)
		count++
	}

	return count, deferred, nil
}

// ElementMapLen returns the size of element map.
func (r *Root) ElementMapLen() int {
	return len(r.elementMap)
}

// GarbageElementLen return the count of removed elements.
func (r *Root) GarbageElementLen() int {
	seen := make(map[string]bool)

	for _, pair := range r.gcElementPairMap {
		seen[pair.elem.CreatedAt().Key()] = true

		if elem, ok := pair.elem.(Container); ok {
			elem.Descendants(func(elem Element, parent Container) bool {
				seen[elem.CreatedAt().Key()] = true
				return false
			})
		}
	}

	return len(seen)
}

// GarbageLen returns the count of removed elements and internal nodes.
func (r *Root) GarbageLen() int {
	return r.GarbageElementLen() + len(r.gcNodePairMap)
}

// RegisterGCPair registers the given pair to hash table.
func (r *Root) RegisterGCPair(pair GCPair) {
	// NOTE(hackerwins): If the child is already registered, it means that the
	// child should be removed from the cache.
	key := keyOf(pair)
	if p, ok := r.gcNodePairMap[key]; ok {
		// An attribute always contributes its own size while it is in the map
		// -- collect reads DataSize, and nothing else's charge covers it once
		// the write that revives it replaces it with a live node. That is not
		// true of a born-dead split piece, whose remaining bytes are inside a
		// sibling's charge, so that one has to give back exactly what
		// registration added.
		if _, isRHTNode := p.Child.(*RHTNode); isRHTNode || p.GCOnlySize == nil {
			r.docSize.GC.Sub(p.Child.DataSize())
		} else {
			r.docSize.GC.Sub(*p.GCOnlySize)
		}

		delete(r.gcNodePairMap, key)
		return
	}

	r.gcNodePairMap[key] = pair

	// GCOnlySize means the child's size was never counted in docSize.Live --
	// it was born removed, or the snapshot-load scan registered it against a
	// root whose Live only counted visible nodes. There is nothing to take
	// out of Live; only the given size enters GC, and purge subtracts the
	// child's size from GC as usual.
	if pair.GCOnlySize != nil {
		r.docSize.GC.Add(*pair.GCOnlySize)
		return
	}

	// Otherwise the child WAS in Live and this registration is what moves it
	// across. Doing both halves here, rather than leaving the Live side to a
	// second call every caller has to remember, is what the JS SDK's
	// registerGCPair does -- and forgetting that second call is not a compile
	// error, it is silent drift only a rebuild can see. It cost
	// json.Tree.RemoveStyle exactly that.
	size := pair.Child.DataSize()
	r.docSize.GC.Add(size)
	r.docSize.Live.Sub(size)

	// NOTE(hackerwins): In general cases, when removing a node, its size
	// includes removedAt, so when subtracting the node size from docSize.Live,
	// we need to subtract the removedAt size. However, RHTNode doesn't have
	// removedAt, so we don't need to subtract it from the Live size.
	if _, isRHTNode := pair.Child.(*RHTNode); !isRHTNode {
		r.docSize.Live.Meta += time.TicketSize
	}
}

// DropGCPair removes the pending GC pair of a child that has left the
// document without being purged, such as a detached node a restore forgot,
// taking out of GC what purging the child would have: its size. That is
// what GC holds for it by the time a node can be forgotten, GCOnlySize or
// not. A piece split off a removed node is booked with GCOnlySize, its split
// overhead alone, while the bytes it took from its sibling stay in GC under
// the sibling's charge; the sibling's own purge gives back only its own
// size, which leaves those bytes for the piece's purge, by its size, to
// give back.
//
// RegisterGCPair's cancel branch (#1874) gives back GCOnlySize instead, for
// a split piece registered a second time under its key, which keeps the
// piece's content bytes with the sibling that carried them in. The two
// differ because they end different things: a cancel, as #1874 has it,
// takes the pair off a piece whose content its sibling's charge is taken to
// cover still, while a drop takes off one whose node has left the document,
// as a purge would, after the sibling's purge may already have given back
// only the sibling's own size. No path of this change reaches
// that cancel for a split piece: a forgotten node's pair is dropped before
// anything registers under its key. It is a no-op, reporting false, when no
// pair is registered under the key, or the one registered is another
// child's, and the child holds no size in retainedOff either.
func (r *Root) DropGCPair(pair GCPair) bool {
	dropped := false
	key := keyOf(pair)
	if p, ok := r.gcNodePairMap[key]; ok && p.Child == pair.Child {
		r.docSize.GC.Sub(p.Child.DataSize())
		delete(r.gcNodePairMap, key)
		dropped = true
	}
	// Held aside under a key another pair took (see bookRetained), and
	// redeemed whether or not a pair of the node's own sits under it since.
	if node, isNode := pair.Child.(*TreeNode); isNode {
		if size, held := r.retainedOff[node]; held {
			r.docSize.GC.Sub(size)
			delete(r.retainedOff, node)
			dropped = true
		}
	}
	return dropped
}

// UnregisterGCPair removes a GC pair whose child has been restored
// (un-tombstoned) by an identity-preserving undo, moving its size GC→Live.
func (r *Root) UnregisterGCPair(pair GCPair) {
	key := keyOf(pair)
	_, ok := r.gcNodePairMap[key]
	if !ok {
		return
	}

	// NOTE: Unlike RegisterGCPair, GCOnlySize doesn't apply here. It only
	// exists to avoid double-counting data at registration time, when a
	// split-born child's content was already counted via a sibling's
	// existing registration. Once registered, this entry's contribution
	// to docSize.GC is always the child's own current DataSize() — that's
	// what must come back out, regardless of how it went in.
	//
	// The caller clears removedAt before calling this, so DataSize() no
	// longer includes the removedAt ticket that WAS counted while it was
	// still registered. Add it back explicitly so GC doesn't retain a
	// stale ticket's worth of residue.
	r.docSize.GC.Sub(pair.Child.DataSize())
	if _, isRHTNode := pair.Child.(*RHTNode); !isRHTNode {
		r.docSize.GC.Meta -= time.TicketSize
	}

	delete(r.gcNodePairMap, key)
}

// Acc accumulates the given DataSize to Live.
func (r *Root) Acc(diff resource.DataSize) {
	r.docSize.Live.Add(diff)
}

// ChargeTreeOp starts an operation on tree's content and returns the func
// that ends it, for a defer: whatever the operation nets in Live goes to
// the tree's GC charge when the tree is removed (see ChargeToRemovedElement).
// Every path that writes tree content calls it, the local proxy and the
// operations a change replays alike, so the root and its clone book the
// same operation the same way.
func (r *Root) ChargeTreeOp(tree *Tree) func() {
	liveBefore := r.docSize.Live
	return func() { r.ChargeToRemovedElement(tree, liveBefore) }
}

// ChargeToRemovedElement moves what Live gained or lost since liveBefore to
// the GC charge of elem when elem is itself in GC, removed or under a
// removed container: its content's bytes are in that charge, as a rebuild,
// which counts a removed element in GC whole, has them, so an operation on
// the content, a revive, a recreate, an insert or a style, moves them there
// and not to Live (yorkie#2251). Pairs the operation booked keep their own
// part of GC; only the Live side moves. A live element is left as it is.
func (r *Root) ChargeToRemovedElement(elem Element, liveBefore resource.DataSize) {
	charged, ok := r.sizeInGC[elem]
	if !ok {
		return
	}
	if tree, isTree := elem.(*Tree); isTree && tree.collected {
		// Defensive. An operation resolves its Tree through elementMap, and a
		// collected tree has left it, a released one has its slot taken over
		// by the restored copy, so none reaches one here. Were one to, its
		// content would be charged to neither side, as release left it.
		r.docSize.Live = liveBefore
		return
	}
	delta := r.docSize.Live
	delta.Sub(liveBefore)
	r.docSize.Live = liveBefore
	r.docSize.GC.Add(delta)
	charged.Add(delta)
	r.sizeInGC[elem] = charged
}

// AccMovedElement books the diff a move stamped on the given element into the
// ledger that is actually holding that element's bytes.
//
// A live element is charged to Live like any other write. An element that is
// already a tombstone -- the ordinary outcome of a concurrent remove and move --
// is not: a rebuild charges a tombstone's whole DataSize, movedAt included, to
// GC, so Live would keep the ticket forever while a rebuild put it in GC.
//
// The recorded charge has to rise with it. deregisterElement subtracts exactly
// what sizeInGC recorded when the tombstone is collected, so a GC top-up that
// left the record alone would only move the stray bytes from Live to GC. An
// element with no record is one Live is still holding (deregisterElement makes
// the same split), so it is charged to Live.
func (r *Root) AccMovedElement(elem Element, diff resource.DataSize) {
	if elem == nil || elem.RemovedAt() == nil {
		r.docSize.Live.Add(diff)
		return
	}

	charged, ok := r.sizeInGC[elem]
	if !ok {
		r.docSize.Live.Add(diff)
		return
	}

	r.docSize.GC.Add(diff)
	charged.Add(diff)
	r.sizeInGC[elem] = charged
}

// AccGC accumulates the given DataSize to GC.
//
// docSize.GC has to stay equal to the sum of the CURRENT DataSize of every
// registered pair's child, because collect subtracts exactly that when it
// purges one. Registration alone cannot maintain that: writing an attribute
// onto a node that is already a tombstone changes the size of a child that
// was registered earlier, with no pair of its own to carry the difference.
// That is what this reports.
func (r *Root) AccGC(diff resource.DataSize) {
	r.docSize.GC.Add(diff)
}

// GCElementPairMap returns the gcElementPairMap for testing purposes.
func (r *Root) GCElementPairMap() map[string]ElementPair {
	return r.gcElementPairMap
}
