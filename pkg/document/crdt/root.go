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

// gcParentsAreComparable is never called. It exists so that making one of the
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
	// again has to know Live is not the side to take it from.
	sizeInGC map[Element]resource.DataSize
}

// NewRoot creates a new instance of Root.
func NewRoot(root *Object) *Root {
	r := &Root{
		elementMap:       make(map[string]Element),
		gcElementPairMap: make(map[string]ElementPair),
		gcNodePairMap:    make(map[gcPairKey]GCPair),
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
	r.RegisterElement(root, nil)

	// NOTE(hackerwins): tombstoned elements are not re-registered here:
	// RegisterElement above already booked every one of them into GC.
	root.Descendants(func(elem Element, parent Container) bool {
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
		return false
	})

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
}

// registerLive registers the given element and its descendants to the element
// map, and charges docSize.Live for each.
func (r *Root) registerLive(element Element) {
	r.elementMap[element.CreatedAt().Key()] = element
	r.docSize.Live.Add(element.DataSize())

	if container, ok := element.(Container); ok {
		container.Descendants(func(elem Element, _ Container) bool {
			r.elementMap[elem.CreatedAt().Key()] = elem
			r.docSize.Live.Add(elem.DataSize())
			return false
		})
	}
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
// Collection entries belonging to the subtree's own tombstones go with it, for
// the same reason: nothing can reach them to collect them, so leaving them
// would hold GarbageLen above zero forever and let a later pass subtract a cost
// this call has already released.
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
	// its own and is still charged normally.
	r.sizeInGC[elem] = resource.DataSize{}

	if pair, ok := r.gcElementPairMap[createdAt]; ok && pair.elem == elem {
		delete(r.gcElementPairMap, createdAt)
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
func (r *Root) DeepCopy() (*Root, error) {
	copiedObject, err := r.object.DeepCopy()
	if err != nil {
		return nil, err
	}
	return NewRoot(copiedObject.(*Object)), nil
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
func (r *Root) GarbageCollect(vector time.VersionVector) (int, error) {
	count := 0

	for {
		purged, deferred, err := r.collect(vector)
		if err != nil {
			return 0, err
		}
		count += purged

		if purged == 0 || deferred == 0 {
			return count, nil
		}
	}
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
		if !vector.EqualToOrAfter(pair.Child.RemovedAt()) {
			continue
		}

		if parent, ok := pair.Parent.(GCBarrier[GCChild]); ok {
			if at := parent.PurgeBarrierAt(pair.Child); at != nil && !vector.EqualToOrAfter(at) {
				deferred++
				continue
			}
		}

		if err := pair.Parent.Purge(pair.Child); err != nil {
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
