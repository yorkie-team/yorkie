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

package crdt

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"

	"github.com/yorkie-team/yorkie/pkg/document/resource"
	"github.com/yorkie-team/yorkie/pkg/document/time"
	"github.com/yorkie-team/yorkie/pkg/llrb"
	"github.com/yorkie-team/yorkie/pkg/splay"
)

// RestoreMode selects the identity-preserving path for reviving or
// re-removing a run of Text characters under their original identities.
//
// This is a server-side CRDT primitive, not a server-side undo/redo
// implementation: the server only applies whichever mode the wire
// operation already specifies. Deciding *when* to revive or re-remove
// content (history stacks, position reconciliation, etc.) is entirely a
// client responsibility, per docs/design/undo-redo.md's stated
// non-goal of server-side undo/redo.
type RestoreMode int

const (
	// RestoreModeNone means an ordinary edit (no restore semantics).
	RestoreModeNone RestoreMode = iota
	// RestoreModeRestore re-establishes removed characters under their
	// original identities.
	RestoreModeRestore
	// RestoreModeRetombstone re-removes previously restored characters
	// under their original identities.
	RestoreModeRetombstone
)

// RestoreSpan carries a run of characters from a single original text
// insertion, addressed by split-invariant absolute offsets [Start, End),
// with a deep copy of the removed value so restore is independent of GC
// state. This is the operation-layer form built from protobuf; the RGA
// primitive slices Content per gap when recreating purged nodes.
type RestoreSpan struct {
	CreatedAt  *time.Ticket
	Start      int
	End        int
	Content    string
	Attributes map[string]string
}

// restoreSpanValue is the internal form of a RestoreSpan carrying a real
// value V so recreation can reuse the value's own Split for sub-slicing.
type restoreSpanValue[V RGATreeSplitValue] struct {
	createdAt *time.Ticket
	start     int
	end       int
	value     V
}

// LedgerCancel is an undo that cancelled one deletion of a node.
type LedgerCancel struct {
	Deletion    *time.Ticket
	CancelledAt *time.Ticket
}

var (
	initialNodeID = NewRGATreeSplitNodeID(time.InitialTicket, 0)
)

// RGATreeSplitValue is a value of RGATreeSplitNode.
// gcAttrSource is implemented by a split value that carries attributes of its
// own. Splitting deep-copies them, so the copy of a TOMBSTONED attribute is new
// garbage sitting under a new parent, with no registration of its own -- the
// original's pair names the original's parent. Without this the copy can never
// be collected (#2002).
type gcAttrSource interface {
	GCParent
	removedAttrs() []*RHTNode
}

type RGATreeSplitValue interface {
	Split(offset int) RGATreeSplitValue
	Len() int
	DeepCopy() RGATreeSplitValue
	String() string
	Marshal() string
	toTestString() string
	DataSize() resource.DataSize
}

// RGATreeSplitNodeID is an ID of RGATreeSplitNode. It is immutable and safe
// for concurrent use: key caches its string atomically on the first call.
type RGATreeSplitNodeID struct {
	createdAt *time.Ticket
	offset    int

	// cachedKey is the cache of the string representation of the ID. It holds
	// a string rather than a *string so that reflect.DeepEqual keeps
	// comparing two IDs by their key, not by the address of a box. See the
	// note on time.Ticket.cachedKey.
	cachedKey atomic.Value
}

// NewRGATreeSplitNodeID creates a new instance of RGATreeSplitNodeID.
func NewRGATreeSplitNodeID(createdAt *time.Ticket, offset int) *RGATreeSplitNodeID {
	return &RGATreeSplitNodeID{
		createdAt: createdAt,
		offset:    offset,
	}
}

// Compare returns an integer comparing two ID. The result will be 0 if
// id==other, -1 if id < other, and +1 if id > other. If the receiver or
// argument is nil, it would panic at runtime. This method is following
// golang standard interface.
func (id *RGATreeSplitNodeID) Compare(other llrb.Key) int {
	if id == nil || other == nil {
		panic("RGATreeSplitNodeID cannot be null")
	}

	o := other.(*RGATreeSplitNodeID)
	compare := id.createdAt.Compare(o.createdAt)
	if compare != 0 {
		return compare
	}

	if id.offset > o.offset {
		return 1
	} else if id.offset < o.offset {
		return -1
	}

	return 0
}

// Equal returns whether the given ID equals or not.
func (id *RGATreeSplitNodeID) Equal(other llrb.Key) bool {
	return id.Compare(other) == 0
}

// CreatedAt returns the creation time of this ID.
func (id *RGATreeSplitNodeID) CreatedAt() *time.Ticket {
	return id.createdAt
}

// Offset returns the offset of this ID.
func (id *RGATreeSplitNodeID) Offset() int {
	return id.offset
}

// Split creates a new ID with an offset from this ID.
func (id *RGATreeSplitNodeID) Split(offset int) *RGATreeSplitNodeID {
	return NewRGATreeSplitNodeID(id.createdAt, id.offset+offset)
}

// ToTestString returns a String containing the metadata of the node id
// for debugging purpose.
func (id *RGATreeSplitNodeID) ToTestString() string {
	return fmt.Sprintf("%s:%d", id.createdAt.ToTestString(), id.offset)
}

func (id *RGATreeSplitNodeID) hasSameCreatedAt(other *RGATreeSplitNodeID) bool {
	return id.createdAt.Compare(other.createdAt) == 0
}

// key returns a string representation of the ID. The result is cached in
// cachedKey, atomically, so the string is built once per ID.
func (id *RGATreeSplitNodeID) key() string {
	if key, ok := id.cachedKey.Load().(string); ok {
		return key
	}

	key := id.createdAt.Key() + ":" + strconv.FormatUint(uint64(id.offset), 10)
	id.cachedKey.Store(key)
	return key
}

// RGATreeSplitNodePos is the position of the text inside the node.
type RGATreeSplitNodePos struct {
	id             *RGATreeSplitNodeID
	relativeOffset int
}

// NewRGATreeSplitNodePos creates a new instance of RGATreeSplitNodePos.
func NewRGATreeSplitNodePos(id *RGATreeSplitNodeID, offset int) *RGATreeSplitNodePos {
	return &RGATreeSplitNodePos{id, offset}
}

func (pos *RGATreeSplitNodePos) getAbsoluteID() *RGATreeSplitNodeID {
	return NewRGATreeSplitNodeID(pos.id.createdAt, pos.id.offset+pos.relativeOffset)
}

// ToTestString returns a String containing the metadata of the position
// for debugging purpose.
func (pos *RGATreeSplitNodePos) ToTestString() string {
	return fmt.Sprintf("%s:%d", pos.id.ToTestString(), pos.relativeOffset)
}

// ID returns the ID of this RGATreeSplitNodePos.
func (pos *RGATreeSplitNodePos) ID() *RGATreeSplitNodeID {
	return pos.id
}

// RelativeOffset returns the relative offset of this RGATreeSplitNodePos.
func (pos *RGATreeSplitNodePos) RelativeOffset() int {
	return pos.relativeOffset
}

// Equal returns whether the given pos equals or not.
func (pos *RGATreeSplitNodePos) Equal(other *RGATreeSplitNodePos) bool {
	if !pos.id.Equal(other.id) {
		return false
	}
	return pos.relativeOffset == other.relativeOffset
}

// RGATreeSplitNode is a node of RGATreeSplit.
type RGATreeSplitNode[V RGATreeSplitValue] struct {
	id        *RGATreeSplitNodeID
	indexNode *splay.Node[*RGATreeSplitNode[V]]
	value     V
	removedAt *time.Ticket

	// The removal ledger. dels are the deletions that removed this node while
	// it was live in the deleter's view; cancels are the deletions undo
	// cancelled. removedAt is the latest uncancelled deletion. !ledgered means
	// the ledger is just removedAt.
	ledgered bool
	dels     []*time.Ticket
	cancels  []LedgerCancel

	prev    *RGATreeSplitNode[V]
	next    *RGATreeSplitNode[V]
	insPrev *RGATreeSplitNode[V]
	insNext *RGATreeSplitNode[V]

	// origin is the insertion context recorded when this node was inserted:
	// the boundary it was inserted after (left) and the character that was to
	// its right (right). origin.Left is nil for nodes created without one
	// (legacy edits, restores, snapshots), which fall back to the
	// createdAt-only RGA rule. Stored inline to avoid an allocation per
	// insert. The IDs it points at are shared, not copied (DeepCopy shares
	// them too), so an origin is never mutated after it is set.
	origin InsertOrigin

	// skelLen > 0 marks a skeleton: a tombstone whose garbage collection has
	// run. Instead of being unlinked, the node drops its content and
	// attributes and keeps only what placement reads -- its ID range
	// [id.offset, id.offset+skelLen), removedAt, origin and its place in the
	// list -- so insertion origins that name it, and the windows integrate
	// scans, resolve the same on replicas that collected it and on replicas
	// that did not (the Yjs GC-struct approach).
	skelLen int
}

// InsertOrigin is the context an insertion was made in. Left is the
// position boundary the content was inserted after, in the same absolute form
// findFloorNodePreferToLeft resolves. Right is the ID of the character that
// was immediately to the right of the caret, or nil at the end of the text.
type InsertOrigin struct {
	Left  *RGATreeSplitNodeID
	Right *RGATreeSplitNodeID
}

// NewRGATreeSplitNode creates a new instance of RGATreeSplit.
func NewRGATreeSplitNode[V RGATreeSplitValue](id *RGATreeSplitNodeID, value V) *RGATreeSplitNode[V] {
	node := &RGATreeSplitNode[V]{
		id:    id,
		value: value,
	}
	node.indexNode = splay.NewNode(node)

	return node
}

// ID returns the ID of this RGATreeSplitNode.
func (s *RGATreeSplitNode[V]) ID() *RGATreeSplitNodeID {
	return s.id
}

// IDString returns the string representation of the ID.
func (s *RGATreeSplitNode[V]) IDString() string {
	return s.id.key()
}

// Origin returns the insertion context of this node, or nil.
func (s *RGATreeSplitNode[V]) Origin() *InsertOrigin {
	if s.origin.Left == nil {
		return nil
	}
	return &s.origin
}

// RawOrigin returns the insertion context as stored, possibly incomplete.
func (s *RGATreeSplitNode[V]) RawOrigin() InsertOrigin { return s.origin }

// SetOrigin sets the insertion context of this node.
func (s *RGATreeSplitNode[V]) SetOrigin(origin InsertOrigin) { s.origin = origin }

// InsPrevID returns previous node ID at the time of this node insertion.
func (s *RGATreeSplitNode[V]) InsPrevID() *RGATreeSplitNodeID {
	if s.insPrev == nil {
		return nil
	}

	return s.insPrev.id
}

func (s *RGATreeSplitNode[V]) contentLen() int {
	if s.skelLen > 0 {
		return s.skelLen
	}
	return s.value.Len()
}

// ContentLen returns the length of the ID range this node covers, whether it
// is live, a tombstone or a skeleton.
func (s *RGATreeSplitNode[V]) ContentLen() int {
	return s.contentLen()
}

// keptAsSkeleton reports that a purge keeps this node as a skeleton, so the
// GC ledger has to read its size before the purge empties it.
func (s *RGATreeSplitNode[V]) keptAsSkeleton() bool { return true }

// IsSkeleton reports whether this node is a garbage-collected tombstone kept
// only for its position.
func (s *RGATreeSplitNode[V]) IsSkeleton() bool {
	return s.skelLen > 0
}

// SkeletonLen returns the length of the ID range a skeleton covers, or 0.
func (s *RGATreeSplitNode[V]) SkeletonLen() int {
	return s.skelLen
}

// SetSkeleton turns this node into a skeleton covering length positions.
// Used when decoding a snapshot.
func (s *RGATreeSplitNode[V]) SetSkeleton(length int) {
	s.skelLen = length
}

// contentLenGreaterThan reports whether contentLen() > n, without a full
// count when the value can answer from its size.
func (s *RGATreeSplitNode[V]) contentLenGreaterThan(n int) bool {
	if s.skelLen > 0 {
		return s.skelLen > n
	}
	if v, ok := any(s.value).(interface{ LenGreaterThan(int) bool }); ok {
		return v.LenGreaterThan(n)
	}
	return s.value.Len() > n
}

// Len returns the length of this node.
func (s *RGATreeSplitNode[V]) Len() int {
	if s.removedAt != nil {
		return 0
	}
	return s.contentLen()
}

// RemovedAt return the remove time of this node.
func (s *RGATreeSplitNode[V]) RemovedAt() *time.Ticket {
	return s.removedAt
}

// SetRemovedAt sets the removal time of this node. It drops the ledger.
func (s *RGATreeSplitNode[V]) SetRemovedAt(removedAt *time.Ticket) {
	s.removedAt = removedAt
	s.ledgered, s.dels, s.cancels = false, nil, nil
}

// Ledger returns the removal ledger: whether there is one, the deletions and
// the cancels.
func (s *RGATreeSplitNode[V]) Ledger() (bool, []*time.Ticket, []LedgerCancel) {
	return s.ledgered, s.dels, s.cancels
}

// RemovalEvents returns the ledger as one list: every removal and every
// cancel's ticket except removedAt, in ticket order. Nil when removedAt says
// it all. An actor's events alternate in ticket order -- it removes a piece
// only while the piece is live in its view, and its undo cancels its one
// uncancelled removal -- so SetRemovalEvents can tell them apart by place.
func (s *RGATreeSplitNode[V]) RemovalEvents() []*time.Ticket {
	if !s.ledgered {
		return nil
	}
	var events []*time.Ticket
	for _, d := range s.dels {
		if s.removedAt == nil || d.Compare(s.removedAt) != 0 {
			events = append(events, d)
		}
	}
	for _, c := range s.cancels {
		events = append(events, c.CancelledAt)
	}
	sort.Slice(events, func(i, j int) bool { return events[i].Compare(events[j]) < 0 })
	return events
}

// SetRemovalEvents rebuilds the ledger from removedAt and the events
// RemovalEvents returned: per actor, in ticket order, a removal then the
// cancel of it. It fails on a repeated ticket, which would pair a cancel
// with a removal that is not older, and when the result does not reproduce
// removedAt, and leaves the node as it was.
func (s *RGATreeSplitNode[V]) SetRemovalEvents(events []*time.Ticket) error {
	if len(events) == 0 {
		return nil
	}
	if err := s.setRemovalEvents(events); err != nil {
		s.ledgered, s.dels, s.cancels = false, nil, nil
		return err
	}
	return nil
}

func (s *RGATreeSplitNode[V]) setRemovalEvents(events []*time.Ticket) error {
	all := append([]*time.Ticket(nil), events...)
	if s.removedAt != nil {
		all = append(all, s.removedAt)
	}
	sort.Slice(all, func(i, j int) bool { return all[i].Compare(all[j]) < 0 })
	for i := 1; i < len(all); i++ {
		if all[i].Compare(all[i-1]) == 0 {
			return fmt.Errorf("removal events repeat a ticket")
		}
	}

	var dels []*time.Ticket
	var cancels []LedgerCancel
	open := map[time.ActorID]*time.Ticket{}
	for _, t := range all {
		if d, ok := open[t.ActorID()]; ok {
			cancels = append(cancels, LedgerCancel{Deletion: d, CancelledAt: t})
			delete(open, t.ActorID())
			continue
		}
		dels = append(dels, t)
		open[t.ActorID()] = t
	}

	removedAt := s.removedAt
	s.ledgered, s.dels, s.cancels = true, dels, cancels
	s.recompute()
	if (removedAt == nil) != (s.removedAt == nil) ||
		(removedAt != nil && removedAt.Compare(s.removedAt) != 0) {
		s.removedAt = removedAt
		return fmt.Errorf("removal events do not reproduce removed_at")
	}
	return nil
}

func (s *RGATreeSplitNode[V]) copyLedger(other *RGATreeSplitNode[V]) {
	s.ledgered = other.ledgered
	s.dels = append([]*time.Ticket(nil), other.dels...)
	s.cancels = append([]LedgerCancel(nil), other.cancels...)
}

// removals returns the deletions in the ledger without building one: a node
// without a ledger was removed by removedAt alone, if at all.
func (s *RGATreeSplitNode[V]) removals() []*time.Ticket {
	if s.ledgered {
		return s.dels
	}
	if s.removedAt != nil {
		return []*time.Ticket{s.removedAt}
	}
	return nil
}

func (s *RGATreeSplitNode[V]) ledger() []*time.Ticket {
	if !s.ledgered {
		s.ledgered = true
		s.dels = nil
		if s.removedAt != nil {
			s.dels = []*time.Ticket{s.removedAt}
		}
	}
	return s.dels
}

func (s *RGATreeSplitNode[V]) isCancelled(d *time.Ticket) bool {
	for _, c := range s.cancels {
		if c.Deletion.Compare(d) == 0 {
			return true
		}
	}
	return false
}

// cancelsByDeletion indexes the ledger's cancels by the deletion each one
// cancels. Ticket.Key is lamport:delimiter:actor, so two tickets share a key
// exactly when Compare says they are equal, and a deletion is cancelled at
// most once (cancelRemoval and setRemovalEvents both pair one to one).
//
// Every walk of the ledger takes this index rather than scanning the cancels
// per deletion: the ledger of a decoded node is as long as whoever sent it
// made it, and a scan per deletion would make reading one quadratic in that
// length.
func (s *RGATreeSplitNode[V]) cancelsByDeletion() map[string]*time.Ticket {
	if len(s.cancels) == 0 {
		return nil
	}
	index := make(map[string]*time.Ticket, len(s.cancels))
	for _, c := range s.cancels {
		index[c.Deletion.Key()] = c.CancelledAt
	}
	return index
}

func (s *RGATreeSplitNode[V]) recompute() {
	cancels := s.cancelsByDeletion()
	var latest *time.Ticket
	for _, d := range s.ledger() {
		if _, ok := cancels[d.Key()]; ok {
			continue
		}
		if latest == nil || d.After(latest) {
			latest = d
		}
	}
	s.removedAt = latest
}

// recordRemoval adds a deletion to the ledger. A node's first removal is
// just removedAt; the ledger is built only when a second removal or a cancel
// needs it.
func (s *RGATreeSplitNode[V]) recordRemoval(removedAt *time.Ticket) {
	if !s.ledgered && s.removedAt == nil {
		s.removedAt = removedAt
		return
	}
	found := false
	cancels := s.cancelsByDeletion()
	for _, d := range s.ledger() {
		if d.Compare(removedAt) == 0 {
			found = true
			break
		}
		// An actor removes a piece only while it is live in its view, so it
		// never holds two uncancelled removals; a change whose vector leaves
		// out its own earlier removal is not honest, and recording it would
		// break the pairing removal_events relies on and grow the ledger.
		if _, cancelled := cancels[d.Key()]; d.ActorID() == removedAt.ActorID() && !cancelled {
			return
		}
	}
	if !found {
		s.dels = append(s.dels, removedAt)
	}
	s.recompute()
}

// cancelRemoval cancels one deletion (an undo of it). Returns whether the
// node is live afterwards.
func (s *RGATreeSplitNode[V]) cancelRemoval(deletion, at *time.Ticket) bool {
	s.ledger()
	if !s.isCancelled(deletion) {
		s.cancels = append(s.cancels, LedgerCancel{Deletion: deletion, CancelledAt: at})
	}
	s.recompute()
	return s.removedAt == nil
}

// pruneStable drops each removal whose cancel is known to every replica, with
// that cancel. A change that arrives later knows both, so the pair never
// decides isRemovedInView again, and dropping it keeps an undo-heavy text
// from carrying every removal it ever had. A removal is older than its
// cancel by the same actor, so the cancel being known is enough. What is
// left is the default ledger once removedAt alone says it.
func (s *RGATreeSplitNode[V]) pruneStable(vector time.VersionVector) {
	var cancels []LedgerCancel
	var dropped map[string]struct{}
	for _, c := range s.cancels {
		if !vector.EqualToOrAfter(c.CancelledAt) {
			cancels = append(cancels, c)
			continue
		}
		if dropped == nil {
			dropped = make(map[string]struct{}, len(s.cancels))
		}
		dropped[c.Deletion.Key()] = struct{}{}
	}
	dels := s.dels
	if len(dropped) > 0 {
		kept := dels[:0:0]
		for _, d := range dels {
			if _, ok := dropped[d.Key()]; !ok {
				kept = append(kept, d)
			}
		}
		dels = kept
	}
	s.dels, s.cancels = dels, cancels
	if len(cancels) == 0 && (len(dels) == 0 ||
		(len(dels) == 1 && s.removedAt != nil && dels[0].Compare(s.removedAt) == 0)) {
		s.ledgered, s.dels = false, nil
	}
}

// uncancelledRemovalBy returns the removal by actor no undo cancelled, if any.
func (s *RGATreeSplitNode[V]) uncancelledRemovalBy(actor time.ActorID) *time.Ticket {
	cancels := s.cancelsByDeletion()
	for _, d := range s.removals() {
		if _, cancelled := cancels[d.Key()]; d.ActorID() == actor && !cancelled {
			return d
		}
	}
	return nil
}

// HeldFromPurge tells whether GC must keep this tombstone under vector, the
// versions every replica has seen. A node removed by two or more deletions
// no undo cancelled stays: an undo of one of them must find it still held by
// the others, and a purged node could not say so. A node whose ledger is not
// yet stable stays too: every deletion and every cancel in it must be known
// to all replicas, so any change that arrives after the purge already knew
// them, and a node a restore recreates with only the removal it cancels
// answers isRemovedInView as the full ledger would. Once that holds and a
// single removal is left, a restore that finds the piece gone can only be
// cancelling that removal, and recreates it.
func (s *RGATreeSplitNode[V]) HeldFromPurge(vector time.VersionVector) bool {
	if !s.ledgered {
		return false
	}
	// A cancelled deletion is older than its cancel by the same actor, so a
	// stable cancel makes it stable too; an uncancelled one is removedAt,
	// which GC checks first, or one of two that keep the node anyway.
	n := 0
	cancels := s.cancelsByDeletion()
	for _, d := range s.dels {
		if _, cancelled := cancels[d.Key()]; !cancelled {
			n++
		}
	}
	for _, c := range s.cancels {
		if !vector.EqualToOrAfter(c.CancelledAt) {
			return true
		}
	}
	return n >= 2
}

// isRemovedInView tells whether the node was removed in the view of the
// given version vector: a deletion it knew that no undo it knew cancelled.
func (s *RGATreeSplitNode[V]) isRemovedInView(vector time.VersionVector) bool {
	// A skeleton's removal was stable everywhere when it was collected; its
	// stand-in removal time is in no vector, so it is answered here.
	if s.skelLen > 0 {
		return true
	}
	cancels := s.cancelsByDeletion()
	for _, d := range s.removals() {
		if !time.TicketKnown(vector, d) {
			continue
		}
		if at, ok := cancels[d.Key()]; ok && time.TicketKnown(vector, at) {
			continue
		}
		return true
	}
	return false
}

// canRemove tells whether a deletion at editedAt may remove (or overwrite
// the tombstone of) this node.
func (s *RGATreeSplitNode[V]) canRemove(editedAt *time.Ticket, creationKnown, tombstoneKnown bool) bool {
	if !creationKnown {
		return false
	}
	// A skeleton's removal was causally stable when it was collected, so no
	// later deletion can be one it has not seen. Its stand-in removal time is
	// not in any vector, so without this a remote deletion would read it as
	// live and record a removal on it. This matches a purged node, which a
	// deletion over it no longer finds.
	if s.skelLen > 0 {
		return false
	}
	if s.removedAt == nil {
		return true
	}
	return !tombstoneKnown && editedAt.After(s.removedAt)
}

// Marshal returns the JSON encoding of this node.
func (s *RGATreeSplitNode[V]) Marshal() string {
	return s.value.Marshal()
}

// String returns the string representation of this node.
func (s *RGATreeSplitNode[V]) String() string {
	return s.value.String()
}

// DataSize returns the data of this node.
func (s *RGATreeSplitNode[V]) DataSize() resource.DataSize {
	dataSize := s.value.DataSize()

	if s.id != nil {
		dataSize.Meta += time.TicketSize
	}

	if s.removedAt != nil {
		dataSize.Meta += time.TicketSize
	}

	return dataSize
}

// DeepCopy returns a new instance of this RGATreeSplitNode without structural info.
func (s *RGATreeSplitNode[V]) DeepCopy() *RGATreeSplitNode[V] {
	node := &RGATreeSplitNode[V]{
		id:        s.id,
		value:     s.value.DeepCopy().(V),
		removedAt: s.removedAt,
		origin:    s.origin,
		skelLen:   s.skelLen,
	}
	node.copyLedger(s)
	node.indexNode = splay.NewNode(node)

	return node
}

// SetInsPrev sets previous node of this node insertion.
func (s *RGATreeSplitNode[V]) SetInsPrev(node *RGATreeSplitNode[V]) {
	s.insPrev = node
	node.insNext = s
}

func (s *RGATreeSplitNode[V]) setPrev(node *RGATreeSplitNode[V]) {
	s.prev = node
	node.next = s
}

func (s *RGATreeSplitNode[V]) split(offset int) *RGATreeSplitNode[V] {
	if s.skelLen > 0 {
		// Skeleton values are empty and never mutated, so both halves share
		// one.
		newNode := NewRGATreeSplitNode(s.id.Split(offset), s.value)
		newNode.removedAt = s.removedAt
		newNode.skelLen = s.skelLen - offset
		s.skelLen = offset
		if s.origin.Left != nil {
			newNode.origin = InsertOrigin{Left: newNode.id, Right: s.origin.Right}
		}
		return newNode
	}
	newNode := NewRGATreeSplitNode(
		s.id.Split(offset),
		s.value.Split(offset).(V),
	)
	newNode.removedAt = s.removedAt
	newNode.copyLedger(s)
	if s.origin.Left != nil {
		// The right piece continues the same insertion: its left origin is
		// the last character of the left piece (resolved through insPrev),
		// and it shares the original right origin.
		newNode.origin = InsertOrigin{Left: newNode.id, Right: s.origin.Right}
	}
	return newNode
}

func (s *RGATreeSplitNode[V]) createdAt() *time.Ticket {
	return s.id.createdAt
}

// toTestString returns a String containing the metadata of the node
// for debugging purpose.
func (s *RGATreeSplitNode[V]) toTestString() string {
	if s.skelLen > 0 {
		return fmt.Sprintf("%s skel(%d)", s.id.ToTestString(), s.skelLen)
	}
	return fmt.Sprintf("%s %s", s.id.ToTestString(), s.value.toTestString())
}

// canStyle checks if node is able to set style.
//
// The only question is whether the styling change knew this node existed. It
// deliberately does NOT ask whether the node has since been removed, and that
// is a convergence requirement rather than a preference: a style is applied
// unconditionally on the replica that issues it -- the node is live there, or
// the range would not have reached it -- and can never be retracted
// afterwards. Every other replica has to apply it too.
//
// Any rule that reads removedAt is delivery-order dependent, because
// removedAt is last-writer-wins and MUTABLE: Remove overwrites it when a
// removal the node has not seen arrives with a later ticket, while a style is
// evaluated once, when it arrives. Two clients deleting the same run
// concurrently plus a third styling over it is enough to make replicas
// disagree, and no single stored ticket fixes it -- the replica cannot know
// which of the concurrent removals the styler had seen. The predicate has to
// not depend on removal state at all.
//
// The cost is that a style covers text the same client had already deleted,
// invisibly, so undoing the style and then the deletion brings the text back
// without the attributes it carried. That is the price of the replicas
// agreeing.
func (s *RGATreeSplitNode[V]) canStyle(vector time.VersionVector) bool {
	if s.skelLen > 0 {
		// Collected: there is no value to style, as there is no node in main.
		return false
	}
	return time.TicketKnown(vector, s.createdAt())
}

// Value returns the value of this node.
func (s *RGATreeSplitNode[V]) Value() V {
	return s.value
}

// RGATreeSplit is a block-based list with improved index-based lookup in RGA.
// The difference from RGATreeList is that it has data on a block basis to
// reduce the size of CRDT metadata. When an Edit occurs on a block,
// the block is split.
type RGATreeSplit[V RGATreeSplitValue] struct {
	initialHead *RGATreeSplitNode[V]
	treeByIndex *splay.Tree[*RGATreeSplitNode[V]]
	treeByID    *llrb.Tree[*RGATreeSplitNodeID, *RGATreeSplitNode[V]]

	// pendingGCPairs buffers GC pairs for nodes that were created
	// already-tombstoned by splitting a removed node. Such pieces inherit
	// removedAt without ever passing through Remove(), so they would
	// otherwise never be registered for GC. edit, Text.Style and
	// Text.RemoveStyle drain this buffer into the GC pairs they return.
	pendingGCPairs []GCPair

	// cancelled holds the nodes whose ledger has a cancel, the only ones
	// PruneLedgers can drop anything from. Nil until first needed: a tree
	// decoded from a snapshot or deep-copied fills it with one scan.
	cancelled map[*RGATreeSplitNode[V]]struct{}

	// scanOrder is scratch space for integrate, reused across inserts.
	scanOrder map[*RGATreeSplitNode[V]]int
	// scanByID maps the start ID of each piece in the integrate window to
	// its order, so siblings' right origins inside the window resolve
	// without an index lookup. Reused across inserts.
	scanByID map[pieceKey]int

	// skelValue is the empty value every skeleton shares. Built lazily.
	skelValue    V
	hasSkelValue bool
}

// trackCancelled records that node's ledger has a cancel.
func (s *RGATreeSplit[V]) trackCancelled(node *RGATreeSplitNode[V]) {
	if s.cancelled == nil {
		s.scanCancelled()
	}
	s.cancelled[node] = struct{}{}
}

func (s *RGATreeSplit[V]) scanCancelled() {
	s.cancelled = make(map[*RGATreeSplitNode[V]]struct{})
	for node := s.initialHead; node != nil; node = node.next {
		if len(node.cancels) > 0 {
			s.cancelled[node] = struct{}{}
		}
	}
}

// HasCancelledRemovals reports whether any node's ledger holds a cancel.
func (s *RGATreeSplit[V]) HasCancelledRemovals() bool {
	if s.cancelled == nil {
		s.scanCancelled()
	}
	return len(s.cancelled) > 0
}

// PruneLedgers drops, from every node's ledger, the removals whose cancels
// the given vector covers (see pruneStable). It returns how many nodes still
// hold a cancel.
func (s *RGATreeSplit[V]) PruneLedgers(vector time.VersionVector) int {
	if s.cancelled == nil {
		s.scanCancelled()
	}
	for node := range s.cancelled {
		node.pruneStable(vector)
		if len(node.cancels) == 0 {
			delete(s.cancelled, node)
		}
	}
	return len(s.cancelled)
}

// NewRGATreeSplit creates a new instance of RGATreeSplit.
func NewRGATreeSplit[V RGATreeSplitValue](initialHead *RGATreeSplitNode[V]) *RGATreeSplit[V] {
	treeByIndex := splay.NewTree(initialHead.indexNode)
	treeByID := llrb.NewTree[*RGATreeSplitNodeID, *RGATreeSplitNode[V]]()
	treeByID.Put(initialHead.ID(), initialHead)

	return &RGATreeSplit[V]{
		initialHead: initialHead,
		treeByIndex: treeByIndex,
		treeByID:    treeByID,
	}
}

func (s *RGATreeSplit[V]) createRange(from, to int) (*RGATreeSplitNodePos, *RGATreeSplitNodePos, error) {
	fromPos, err := s.findNodePos(from)
	if err != nil {
		return nil, nil, err
	}
	if from == to {
		return fromPos, fromPos, nil
	}

	toPos, err := s.findNodePos(to)
	if err != nil {
		return nil, nil, err
	}

	return fromPos, toPos, nil
}

func (s *RGATreeSplit[V]) findNodePos(index int) (*RGATreeSplitNodePos, error) {
	splayNode, offset, err := s.treeByIndex.FindForText(index)
	if err != nil {
		return nil, err
	}
	node := splayNode.Value()
	return &RGATreeSplitNodePos{
		id:             node.ID(),
		relativeOffset: offset,
	}, nil
}

func (s *RGATreeSplit[V]) findNodeWithSplit(
	pos *RGATreeSplitNodePos,
	updatedAt *time.Ticket,
) (*RGATreeSplitNode[V], *RGATreeSplitNode[V], resource.DataSize, error) {
	left, right, diff, _, err := s.findNodeWithSplitAt(pos, updatedAt)
	return left, right, diff, err
}

// findNodeWithSplitAt is findNodeWithSplit that also reports whether the
// returned left node is the one ending exactly at pos, i.e. the createdAt
// skip loop did not move past any node.
func (s *RGATreeSplit[V]) findNodeWithSplitAt(
	pos *RGATreeSplitNodePos,
	updatedAt *time.Ticket,
) (*RGATreeSplitNode[V], *RGATreeSplitNode[V], resource.DataSize, bool, error) {
	absoluteID := pos.getAbsoluteID()
	node, err := s.findFloorNodePreferToLeft(absoluteID)
	if err != nil {
		return nil, nil, resource.DataSize{}, false, err
	}

	relativeOffset := absoluteID.offset - node.id.offset

	_, diff, err := s.splitNode(node, relativeOffset)
	if err != nil {
		return nil, nil, resource.DataSize{}, false, err
	}

	atBoundary := true
	for node.next != nil && node.next.createdAt().After(updatedAt) {
		node = node.next
		atBoundary = false
	}

	return node, node.next, diff, atBoundary, nil
}

// resolveLeft resolves a scanned node's left origin. A boundary equal to the
// inserting node's own is answered without a lookup.
func (s *RGATreeSplit[V]) resolveLeft(
	id, mine *RGATreeSplitNodeID,
	left *RGATreeSplitNode[V],
) (*RGATreeSplitNode[V], error) {
	if sameBoundary(id, mine) {
		return left, nil
	}
	return s.findFloorNodePreferToLeft(id)
}

// RightOriginOf returns the right origin a local insert at the given
// boundary records: the node right after it, tombstones included, or nil at
// the end of the text. A receiver that has purged it recovers it from the
// change's version vector (see integrate).
func (s *RGATreeSplit[V]) RightOriginOf(absoluteID *RGATreeSplitNodeID) (*RGATreeSplitNodeID, error) {
	node, err := s.findFloorNodePreferToLeft(absoluteID)
	if err != nil {
		return nil, err
	}
	relativeOffset := absoluteID.offset - node.id.offset
	if node.contentLenGreaterThan(relativeOffset) {
		// Inside one insertion: the right neighbour continues it, which is
		// always a sibling at this boundary.
		return NewRGATreeSplitNodeID(node.id.createdAt, absoluteID.offset), nil
	}
	if node.next == nil {
		return nil, nil
	}
	return node.next.id, nil
}

// sameBoundary reports whether two left origins name the same boundary.
// Boundaries are canonical: "after character (C, k)" is always (C, k+1).
func sameBoundary(a, b *RGATreeSplitNodeID) bool {
	return a != nil && b != nil && a.offset == b.offset && a.createdAt.Compare(b.createdAt) == 0
}

// integrate returns the node a new insertion goes after, using both origins
// (the YjsMod / FugueMax rule). left is the piece ending at the insertion
// boundary; right is the piece starting at the right origin. Only nodes
// between the two can be concurrent with the insertion, so the scan is
// bounded by that window and is empty when nothing was inserted there
// concurrently.
func (s *RGATreeSplit[V]) integrate(
	origin InsertOrigin,
	editedAt *time.Ticket,
	vv time.VersionVector,
) (*RGATreeSplitNode[V], error) {
	left, err := s.findFloorNodePreferToLeft(origin.Left)
	if err != nil {
		return nil, err
	}
	right, err := s.originRightNode(origin.Right)
	rightResolved := err == nil
	if err != nil {
		if vv == nil {
			return nil, err
		}
		// The right origin was purged. It was the first node after left
		// that the author knew; every node between them is concurrent. So
		// the first node after left that the change's version vector
		// covers is the same boundary.
		right = left.next
		for right != nil && !vv.EqualToOrAfter(right.createdAt()) {
			right = right.next
		}
	}
	if left.next == right && right != nil {
		return left, nil
	}
	if origin.Right == nil && right == nil {
		return s.integrateOpen(left, origin, editedAt)
	}

	// Order of the nodes in the window (left, right]. A right origin that is
	// not in it lies past right. The map is reused across inserts.
	if s.scanOrder == nil {
		s.scanOrder = make(map[*RGATreeSplitNode[V]]int)
	}
	order := s.scanOrder
	clear(order)
	if s.scanByID == nil {
		s.scanByID = make(map[pieceKey]int)
	}
	byID := s.scanByID
	clear(byID)
	order[left] = 0
	i := 1
	for n := left.next; n != nil && n != right; n = n.next {
		order[n] = i
		byID[pieceKeyOf(n.id)] = i
		i++
	}
	rightIdx := i
	if right != nil {
		order[right] = rightIdx
	}
	compareRight := func(n *RGATreeSplitNode[V]) int {
		if n == right {
			return 0
		}
		if idx, ok := order[n]; ok && n != nil {
			if idx < rightIdx {
				return -1
			}
			return 0
		}
		return 1
	}

	dest := left
	prev := left
	scanning := false
	for o := left.next; ; o = o.next {
		if !scanning {
			dest = prev
		}
		if o == nil || o == right {
			break
		}
		prev = o
		if o.origin.Left == nil {
			break
		}
		oleft, err := s.resolveLeft(o.origin.Left, origin.Left, left)
		if err != nil {
			return nil, err
		}
		if oleft == left {
			// Siblings typed against the same right neighbour share the
			// right origin, and a right origin that starts a piece inside
			// the window is in byID; answer both without an index lookup.
			var cmp int
			if rightResolved && o.origin.Right != nil && origin.Right != nil && o.origin.Right.Equal(origin.Right) {
				cmp = 0
			} else if idx, ok := byID[pieceKeyOf0(o.origin.Right)]; ok && o.origin.Right != nil {
				if idx < rightIdx {
					cmp = -1
				}
			} else {
				oright, err := s.originRightNode(o.origin.Right)
				if err != nil {
					return nil, err
				}
				cmp = compareRight(oright)
			}
			switch {
			case cmp < 0:
				scanning = true
			case cmp == 0:
				if editedAt.ActorID().Compare(o.createdAt().ActorID()) < 0 {
					return dest, nil
				}
				scanning = false
			default:
				scanning = false
			}
			continue
		}
		if _, inWindow := order[oleft]; !inWindow {
			// oleft < left: o belongs to an earlier insertion point.
			break
		}
		// oleft > left: o hangs off something inside the window; skip it.
	}
	return dest, nil
}

// integrateOpen places an insertion that has no right origin. It scans
// forward from left while nodes hang off this boundary or off nodes already
// scanned, and orders siblings that also have no right origin by actor; a
// sibling with a right origin is treated as tighter-bound and skipped past.
func (s *RGATreeSplit[V]) integrateOpen(
	left *RGATreeSplitNode[V],
	origin InsertOrigin,
	editedAt *time.Ticket,
) (*RGATreeSplitNode[V], error) {
	if s.scanOrder == nil {
		s.scanOrder = make(map[*RGATreeSplitNode[V]]int)
	}
	seen := s.scanOrder
	clear(seen)
	seen[left] = 0
	dest, prev := left, left
	scanning := false
	for o := left.next; ; o = o.next {
		if !scanning {
			dest = prev
		}
		if o == nil {
			break
		}
		prev = o
		seen[o] = 1
		if o.origin.Left == nil {
			break
		}
		oleft, err := s.resolveLeft(o.origin.Left, origin.Left, left)
		if err != nil {
			return nil, err
		}
		if oleft == left {
			if o.origin.Right != nil {
				// o is bound before some node; it sits closer to left.
				scanning = true
				continue
			}
			if editedAt.ActorID().Compare(o.createdAt().ActorID()) < 0 {
				return dest, nil
			}
			scanning = false
			continue
		}
		if _, ok := seen[oleft]; !ok {
			break
		}
	}
	return dest, nil
}

// isUncontested reports, without any index lookup, whether the insertion
// point found by findNodeWithSplit is already the left origin with the
// right origin directly after it. That is the case for every local insert
// and for any remote insert nothing was inserted next to concurrently, and
// there the two-origin rule has nothing to decide.
func (s *RGATreeSplit[V]) isUncontested(
	left, right *RGATreeSplitNode[V],
	origin InsertOrigin,
	leftIsOrigin bool,
) bool {
	if !leftIsOrigin && (left.id.createdAt.Compare(origin.Left.createdAt) != 0 ||
		left.id.offset+left.contentLen() != origin.Left.offset) {
		return false
	}
	if origin.Right == nil {
		// No right constraint: the scan stops at the first node that does
		// not hang off this boundary. If the node right after the boundary
		// is not a sibling, that is where it stops.
		return right == nil || right.origin.Left == nil || !sameBoundary(right.origin.Left, origin.Left)
	}
	if right == nil {
		return false
	}
	return right.id.Equal(origin.Right)
}

// originRightNode returns the piece starting at the given right origin,
// splitting a piece if needed; nil means the end of the text.
func (s *RGATreeSplit[V]) originRightNode(id *RGATreeSplitNodeID) (*RGATreeSplitNode[V], error) {
	if id == nil {
		return nil, nil
	}
	node := s.findFloorNode(id)
	if node == nil {
		return nil, fmt.Errorf("right origin not found: %s", id.ToTestString())
	}
	if node.id.offset == id.offset {
		return node, nil
	}
	split, _, err := s.splitNode(node, id.offset-node.id.offset)
	return split, err
}

func (s *RGATreeSplit[V]) findFloorNodePreferToLeft(id *RGATreeSplitNodeID) (*RGATreeSplitNode[V], error) {
	node := s.findFloorNode(id)
	if node == nil {
		return nil, fmt.Errorf("the node of the given id should be found: %s", s.ToTestString())
	}

	if id.offset > 0 && node.id.offset == id.offset {
		// NOTE: InsPrev may not be present due to GC.
		if node.insPrev == nil {
			return node, nil
		}
		node = node.insPrev
	}

	return node, nil
}

// normalizePos converts a local position (id, relativeOffset) into a single
// absolute offset measured from the head (0:0) of the physical chain, so the
// position keeps meaning the same place regardless of how the chain is split
// later.
//
// JS sums the length of every node on the prev chain. Go reads the same sum
// from treeByIndex instead: every node on the chain is in it, in chain order,
// weighted by its live length (a tombstone stays in with weight zero; only
// Purge takes a node out, and Purge unlinks it from the chain too). A local
// edit normalizes its position to build its reverse operation, so a linear
// walk here makes typing a document quadratic.
func (s *RGATreeSplit[V]) normalizePos(pos *RGATreeSplitNodePos) (*RGATreeSplitNodePos, error) {
	node := s.findFloorNode(pos.id)
	if node == nil {
		return nil, fmt.Errorf("the node of the given id should be found: %s", pos.ToTestString())
	}

	index := s.treeByIndex.IndexOf(node.indexNode)
	if index < 0 {
		return nil, fmt.Errorf("the node of the given id should be indexed: %s", pos.ToTestString())
	}

	return NewRGATreeSplitNodePos(s.initialHead.id, index+pos.relativeOffset), nil
}

// refinePos remaps the given position onto the current split chain.
//
//   - Traverses the physical next chain (not insNext).
//   - Counts only live characters: removed nodes have length 0, so they are
//     stepped over without consuming any of the offset.
//   - If the offset exceeds the length of the current node, it moves forward
//     through next nodes, subtracting lengths, until the offset fits.
//   - If it runs out of nodes, it snaps to the end of the last one.
//
// For example, a position (1:2:0, rel=5) taken before ["12345"](1:2:0) was
// split into ["1"](1:2:0) ["23"](1:2:1) ["45"](1:2:3) refines to (1:2:3,
// rel=2), the same place in the text.
//
// A pure split preserves the place, as above; a deletion does not. The offset
// itself is preserved -- it is only reinterpreted against the live text -- and
// removed runs contribute zero length, so they are skipped without consuming
// any of it. The position therefore ends up naming a character further along
// than the one it originally sat beside. For example, in ["01"]{"23"}["456789"]
// offset 4 named the boundary after "3" before "23" was removed, and names the
// boundary after "5" now.
func (s *RGATreeSplit[V]) refinePos(pos *RGATreeSplitNodePos) (*RGATreeSplitNodePos, error) {
	node := s.findFloorNode(pos.id)
	if node == nil {
		return nil, fmt.Errorf("the node of the given id should be found: %s", pos.ToTestString())
	}

	offsetInPart := pos.relativeOffset
	partLen := node.contentLen()
	for offsetInPart > partLen {
		offsetInPart -= partLen

		next := node.next
		if next == nil {
			return NewRGATreeSplitNodePos(node.id, partLen), nil
		}

		node = next
		partLen = node.Len()
	}

	return NewRGATreeSplitNodePos(node.id, offsetInPart), nil
}

func (s *RGATreeSplit[V]) splitNode(
	node *RGATreeSplitNode[V],
	offset int,
) (*RGATreeSplitNode[V], resource.DataSize, error) {
	var diff resource.DataSize

	if offset < 0 || offset > node.contentLen() {
		return nil, diff, fmt.Errorf("offset should be within the range of length: %s", s.ToTestString())
	}

	if offset == 0 {
		return node, diff, nil
	} else if offset == node.contentLen() {
		return node.next, diff, nil
	}

	if node.skelLen > 0 {
		// A skeleton holds no value and is not in the GC registry: its
		// remainder is a skeleton too and needs neither size accounting nor
		// a GC pair.
		splitNode := node.split(offset)
		s.treeByIndex.UpdateWeight(splitNode.indexNode)
		s.InsertAfter(node, splitNode)
		if insNext := node.insNext; insNext != nil {
			insNext.SetInsPrev(splitNode)
		}
		splitNode.SetInsPrev(node)
		return splitNode, diff, nil
	}

	prevSize := node.DataSize()

	splitNode := node.split(offset)
	if len(splitNode.cancels) > 0 && s.cancelled != nil {
		s.cancelled[splitNode] = struct{}{}
	}
	s.treeByIndex.UpdateWeight(splitNode.indexNode)
	s.InsertAfter(node, splitNode)

	insNext := node.insNext
	if insNext != nil {
		insNext.SetInsPrev(splitNode)
	}
	splitNode.SetInsPrev(node)

	// NOTE(hackerwins): Calculate data size after node splitting:
	// Take the sum of the two split nodes(left and right) minus the size of
	// the original node. This calculates the net metadata overhead added by
	// the split operation.
	diff.Add(node.DataSize(), splitNode.DataSize())
	diff.Sub(prevSize)

	// A split deep-copies the value's attributes, so every tombstone among
	// them is duplicated under the new node. The copy was never in
	// docSize.Live -- DataSize excludes removed attributes -- so it enters GC
	// only, and purge subtracts the same size back out.
	if src, ok := any(splitNode.value).(gcAttrSource); ok {
		for _, attr := range src.removedAttrs() {
			gcSize := attr.DataSize()
			s.pendingGCPairs = append(s.pendingGCPairs, GCPair{
				Parent:     src,
				Child:      attr,
				GCOnlySize: &gcSize,
			})
		}
	}

	// NOTE: A piece split off an already-tombstoned node inherits
	// removedAt without going through Remove(), so no GC pair is created
	// for it in the normal deletion path. Buffer one here so it can be
	// purged; otherwise it stays in the list forever. The piece was never
	// live, so the net-new size the split created goes straight to
	// docSize.GC when the pair is registered; return a zero diff to the
	// caller (which accounts diffs to docSize.Live).
	if splitNode.removedAt != nil {
		gcSize := diff
		s.pendingGCPairs = append(s.pendingGCPairs, GCPair{
			Parent:     s,
			Child:      splitNode,
			GCOnlySize: &gcSize,
		})
		return splitNode, resource.DataSize{}, nil
	}

	return splitNode, diff, nil
}

// drainPendingGCPairs returns the GC pairs buffered for born-tombstoned
// split pieces and clears the buffer.
func (s *RGATreeSplit[V]) drainPendingGCPairs() []GCPair {
	pairs := s.pendingGCPairs
	s.pendingGCPairs = nil
	return pairs
}

// InsertAfter inserts the given node after the given previous node.
func (s *RGATreeSplit[V]) InsertAfter(prev, node *RGATreeSplitNode[V]) *RGATreeSplitNode[V] {
	next := prev.next
	node.setPrev(prev)
	if next != nil {
		next.setPrev(node)
	}

	s.treeByID.Put(node.id, node)
	s.treeByIndex.InsertAfter(prev.indexNode, node.indexNode)

	return node
}

// NextOf returns the node after the given node in the list, or nil.
func (s *RGATreeSplit[V]) NextOf(node *RGATreeSplitNode[V]) *RGATreeSplitNode[V] {
	return node.next
}

// InitialHead returns the head node of this RGATreeSplit.
func (s *RGATreeSplit[V]) InitialHead() *RGATreeSplitNode[V] {
	return s.initialHead
}

// FindNode returns the node of the given ID.
func (s *RGATreeSplit[V]) FindNode(id *RGATreeSplitNodeID) *RGATreeSplitNode[V] {
	if id == nil {
		return nil
	}

	return s.findFloorNode(id)
}

// CheckWeight returns false when there is an incorrect weight node.
// for debugging purpose.
func (s *RGATreeSplit[V]) CheckWeight() bool {
	return s.treeByIndex.CheckWeight()
}

func (s *RGATreeSplit[V]) findFloorNode(id *RGATreeSplitNodeID) *RGATreeSplitNode[V] {
	key, value := s.treeByID.Floor(id)
	if key == nil {
		return nil
	}

	if !key.Equal(id) && !key.hasSameCreatedAt(id) {
		return nil
	}

	return value
}

// edit deletes the given range and, if content is non-empty, inserts it in
// place. Besides the GC pairs for deleted nodes, it also reports those nodes
// as restoreSpanValues (identity + value), captured in the same walk that
// builds the GC pairs, so a caller can revive or re-remove them by identity
// later without a second traversal over the removed nodes.
func (s *RGATreeSplit[V]) edit(
	from *RGATreeSplitNodePos,
	to *RGATreeSplitNodePos,
	content V,
	editedAt *time.Ticket,
	versionVector time.VersionVector,
	origin InsertOrigin,
	local bool,
) (*RGATreeSplitNodePos, []GCPair, resource.DataSize, []restoreSpanValue[V], error) {
	var diff resource.DataSize

	// 01. Split nodes with from and to
	toLeft, toRight, diffTo, err := s.findNodeWithSplit(to, editedAt)
	if err != nil {
		return nil, s.drainPendingGCPairs(), diff, nil, err
	}

	fromLeft, fromRight, diffFrom, fromAtBoundary, err := s.findNodeWithSplitAt(from, editedAt)
	if err != nil {
		diff.Add(diffTo)
		return nil, s.drainPendingGCPairs(), diff, nil, err
	}

	diff.Add(diffTo, diffFrom)

	// 02. delete between from and to
	nodesToDelete := s.findBetween(fromRight, toRight)
	removedNodes := s.deleteNodes(nodesToDelete, editedAt, versionVector)

	var caretID *RGATreeSplitNodeID
	if toRight == nil {
		caretID = toLeft.id
	} else {
		caretID = toRight.id
	}
	caretPos := NewRGATreeSplitNodePos(caretID, 0)

	// 03. insert a new node
	if content.Len() > 0 {
		insertAfter := fromLeft
		// fromLeft ends exactly at the left origin when the skip loop did not
		// move and the origin is from's own boundary, which is how both local
		// edits and decoded remote edits build it. Knowing that here spares
		// isUncontested a length count of fromLeft.
		leftIsOrigin := fromAtBoundary && origin.Left != nil &&
			origin.Left.offset == from.id.offset+from.relativeOffset &&
			origin.Left.createdAt.Compare(from.id.createdAt) == 0
		if origin.Left != nil && !s.isUncontested(fromLeft, fromRight, origin, leftIsOrigin) {
			insertAfter, err = s.integrate(origin, editedAt, versionVector)
			if err != nil {
				return nil, s.drainPendingGCPairs(), diff, nil, err
			}
		}
		node := NewRGATreeSplitNode(NewRGATreeSplitNodeID(editedAt, 0), content)
		node.origin = origin
		inserted := s.InsertAfter(insertAfter, node)
		diff.Add(inserted.DataSize())

		caretPos = NewRGATreeSplitNodePos(inserted.id, inserted.contentLen())
	}

	// 04. add removed node
	var pairs []GCPair
	var removedSpans []restoreSpanValue[V]
	// In document order, not the map's: restore anchors each recreated
	// fragment after the one before it, so the spans of the reverse must
	// arrive left to right.
	for _, removedNode := range nodesToDelete {
		if removedNodes[removedNode.id.key()] != removedNode {
			continue
		}
		pairs = append(pairs, GCPair{
			Parent: s,
			Child:  removedNode,
		})
		// NOTE: value aliases the live node's value rather than deep-copying
		// it (unlike JS's rga_tree_split.ts substring(0), which copies
		// specifically because a later split of the tombstone must not
		// mutate already-captured content — some V.Split implementations,
		// e.g. TextValue, mutate their receiver). Safe today only because
		// every caller (Text.Edit) flattens each span's value into plain
		// strings before returning, with no further split of these nodes
		// happening in between. If a future caller holds onto this
		// restoreSpanValue instead of flattening it immediately, it must
		// deep-copy value first.
		removedSpans = append(removedSpans, restoreSpanValue[V]{
			createdAt: removedNode.createdAt(),
			start:     removedNode.ID().Offset(),
			end:       removedNode.ID().Offset() + removedNode.contentLen(),
			value:     removedNode.Value(),
		})
	}

	pairs = append(pairs, s.drainPendingGCPairs()...)

	return caretPos, pairs, diff, removedSpans, nil
}

func (s *RGATreeSplit[V]) findBetween(from, to *RGATreeSplitNode[V]) []*RGATreeSplitNode[V] {
	current := from
	var nodes []*RGATreeSplitNode[V]
	for current != nil && current != to {
		nodes = append(nodes, current)
		current = current.next
	}
	return nodes
}

func (s *RGATreeSplit[V]) deleteNodes(
	candidates []*RGATreeSplitNode[V],
	editedAt *time.Ticket,
	vector time.VersionVector,
) map[string]*RGATreeSplitNode[V] {
	removed := make(map[string]*RGATreeSplitNode[V])
	if len(candidates) == 0 {
		return removed
	}

	var nodesToKeep []*RGATreeSplitNode[V]
	leftEdge, rightEdge := s.findEdgesOfCandidates(candidates)
	nodesToKeep = append(nodesToKeep, leftEdge)

	isLocal := len(vector) == 0
	for _, n := range candidates {
		// NOTE(hackerwins): Determine if the node's creation event was visible.
		creationKnown := false
		if isLocal {
			creationKnown = true
		} else if l, ok := vector.Get(n.createdAt().ActorID()); ok && l >= n.createdAt().Lamport() {
			creationKnown = true
		}

		// NOTE(hackerwins): Determine if existing tombstone was already causally known.
		tombstoneKnown := false
		if n.removedAt != nil {
			if isLocal {
				tombstoneKnown = true
			} else if l, ok := vector.Get(n.removedAt.ActorID()); ok && l >= n.removedAt.Lamport() {
				tombstoneKnown = true
			}
		}

		// Once a tombstone can be cancelled by an undo, "the deleter knew the
		// node" is not enough -- the node has to have been LIVE in the
		// deleter's view, or a deletion removes text its author never saw.
		liveInView := n.removedAt == nil
		if !isLocal {
			liveInView = creationKnown && !n.isRemovedInView(vector)
		}
		if liveInView && n.canRemove(editedAt, creationKnown, tombstoneKnown) {
			wasLive := n.removedAt == nil
			n.recordRemoval(editedAt)
			if wasLive {
				removed[n.id.key()] = n
				continue
			}
		} else if !isLocal && liveInView && n.removedAt != nil {
			// A node this deletion saw live but someone else removed
			// concurrently records the deletion all the same.
			n.recordRemoval(editedAt)
		}
		nodesToKeep = append(nodesToKeep, n)
	}

	nodesToKeep = append(nodesToKeep, rightEdge)
	s.deleteIndexNodes(nodesToKeep)

	return removed
}

// findEdgesOfCandidates finds the edges outside `candidates`,
// (which has not already been deleted, or be undefined but not yet implemented)
// right edge is undefined means `candidates` contains the end of text.
func (s *RGATreeSplit[V]) findEdgesOfCandidates(
	candidates []*RGATreeSplitNode[V],
) (*RGATreeSplitNode[V], *RGATreeSplitNode[V]) {
	return candidates[0].prev, candidates[len(candidates)-1].next
}

// deleteIndexNodes clears the index nodes of the given deletion boundaries.
// The boundaries mean the nodes that will not be deleted in the range.
func (s *RGATreeSplit[V]) deleteIndexNodes(boundaries []*RGATreeSplitNode[V]) {
	for i := 0; i < len(boundaries)-1; i++ {
		leftBoundary := boundaries[i]
		rightBoundary := boundaries[i+1]
		if leftBoundary.next == rightBoundary {
			// If there is no node to delete between boundaries, do notting.
			continue
		} else if rightBoundary == nil {
			s.treeByIndex.DeleteRange(leftBoundary.indexNode, nil)
		} else {
			s.treeByIndex.DeleteRange(leftBoundary.indexNode, rightBoundary.indexNode)
		}
	}
}

// subValue returns a deep copy of full restricted to [from, to). full is
// left unmodified.
func subValue[V RGATreeSplitValue](full V, from, to int) V {
	cp := full.DeepCopy()
	tail := cp.Split(from) // cp=[0,from), tail=[from,len)
	tail.Split(to - from)  // tail=[from,to), discard [to,len)
	return tail.(V)
}

// restore re-establishes the characters in spans under their ORIGINAL
// identities. Per overlapping region:
//   - live piece exists       → skip (idempotent; another restore already revived it)
//   - tombstoned piece exists → clear removedAt (un-tombstone)
//   - no piece exists (GC'd)  → recreate a node with the original ID
//
// Returns (untombstoned, recreated, stillTombstoned):
//   - untombstoned: nodes flipped tombstone→live (caller GC-unregisters the
//     pre-existing ones and moves their size GC→Live)
//   - recreated: brand-new live nodes for purged regions (caller adds their
//     size to Live)
//   - stillTombstoned: born-tombstoned split remainders (drained
//     pendingGCPairs), including split-born targets so the caller can
//     register them before un-registering (keeps GC accounting balanced)
func (s *RGATreeSplit[V]) restore(
	spans []restoreSpanValue[V],
	fromPos *RGATreeSplitNodePos,
	executedAt *time.Ticket,
) (untombstoned, recreated []*RGATreeSplitNode[V], stillTombstoned []GCPair, err error) {
	// chainAnchor is the node last placed at the current cursor (un-tombstoned
	// or recreated), in document order. When a recreated fragment has no
	// surviving same-insertion anchor, chaining after this keeps a purged
	// multi-fragment run in left-to-right order instead of each fragment
	// prepending at the same fixed fallback (initialHead), which would rebuild
	// the run reversed. Spans arrive in document order, so this is always the
	// recreated fragment's left neighbour.
	var chainAnchor *RGATreeSplitNode[V]
	for _, span := range spans {
		pieces := s.findPiecesOverlapping(span.createdAt, span.start, span.end)

		cursor := span.start
		pieceIdx := 0
		for cursor < span.end {
			var piece *RGATreeSplitNode[V]
			pieceStart, pieceEnd := math.MaxInt, math.MaxInt
			if pieceIdx < len(pieces) {
				piece = pieces[pieceIdx]
				pieceStart = piece.ID().Offset()
				pieceEnd = pieceStart + piece.contentLen()
			}

			if piece != nil && pieceStart <= cursor {
				overlapEnd := min(pieceEnd, span.end)
				if piece.skelLen > 0 {
					// A skeleton is a collected tombstone: its content is
					// gone, as a purged node's is in main. Refill it from the
					// span in place, which is the original slot.
					target, _, isoErr := s.isolateRange(piece, cursor, overlapEnd)
					if isoErr != nil {
						return nil, nil, nil, isoErr
					}
					target.value = subValue(span.value, cursor-span.start, overlapEnd-span.start)
					target.skelLen = 0
					target.SetRemovedAt(nil)
					// The collected removal was stable, as a purged node's
					// is: record the same stand-in removal and cancel as the
					// recreate path below, so the refilled node answers
					// isRemovedInView as a recreated one would.
					stable := time.NewTicket(0, 0, executedAt.ActorID())
					target.recordRemoval(stable)
					target.cancelRemoval(stable, executedAt)
					s.trackCancelled(target)
					s.treeByIndex.Splay(target.indexNode)
					recreated = append(recreated, target)
					chainAnchor = target
				} else if piece.removedAt != nil {
					target, _, isoErr := s.isolateRange(piece, cursor, overlapEnd)
					if isoErr != nil {
						return nil, nil, nil, isoErr
					}
					// An undo reverses its own actor's change, so the removal it
					// cancels is the one uncancelled removal by that actor:
					// one actor cannot remove a piece already removed in its
					// view. No other removal holds it if there is none.
					live := false
					if d := target.uncancelledRemovalBy(executedAt.ActorID()); d != nil {
						live = target.cancelRemoval(d, executedAt)
						s.trackCancelled(target)
					}
					if live {
						s.treeByIndex.Splay(target.indexNode)
						untombstoned = append(untombstoned, target)
					}
					chainAnchor = target
				} else {
					chainAnchor = piece
				}
				cursor = overlapEnd
				if overlapEnd >= pieceEnd {
					pieceIdx++
				}
			} else {
				// A purged piece had a single uncancelled removal, stable on
				// every replica (see HeldFromPurge), so this restore can only
				// be cancelling that one: recreate it.
				gapEnd := min(pieceStart, span.end)
				val := subValue(span.value, cursor-span.start, gapEnd-span.start)
				newNode := NewRGATreeSplitNode(
					NewRGATreeSplitNodeID(span.createdAt, cursor), val)
				// The purged removal was known to every replica before the
				// purge, so every change that can arrive later knows it. A
				// stand-in removal by the same actor at lamport 0, which
				// every such change's vector covers, keeps that answer for
				// isRemovedInView, with this cancel. The pair is dropped once
				// the cancel is known everywhere (pruneStable); until then
				// snapshots carry it, and the JS SDK has to record the same.
				stable := time.NewTicket(0, 0, executedAt.ActorID())
				newNode.recordRemoval(stable)
				newNode.cancelRemoval(stable, executedAt)
				s.trackCancelled(newNode)
				prev := s.findRestoreAnchor(
					span.createdAt, cursor, gapEnd, chainAnchor, fromPos, executedAt)
				s.InsertAfter(prev, newNode)
				// InsertAfter only maintains the physical prev/next chain.
				// Relink the separate insertion chain (insPrev/insNext) too, the
				// same way splitNode does, so a recreated interior fragment is
				// not skipped by the surviving same-insertion neighbours.
				// Otherwise a later edit whose boundary lands on this fragment
				// resolves through a stale insertion pointer in
				// findFloorNodePreferToLeft and miscomputes its offset
				// (yorkie-team/yorkie-js-sdk#1327).
				// The neighbours are the nearest surviving pieces, not just the
				// ones covering the adjacent offsets: with the piece before the
				// gap purged too, the one before that would keep pointing past
				// the new fragment, and purging it later would hand the piece
				// after the gap an insPrev that skips the fragment.
				if cursor > 0 {
					prevID := NewRGATreeSplitNodeID(span.createdAt, cursor-1)
					if key, insPrev := s.treeByID.Floor(prevID); key != nil && key.hasSameCreatedAt(prevID) {
						newNode.SetInsPrev(insPrev)
					}
				}
				nextID := NewRGATreeSplitNodeID(span.createdAt, gapEnd)
				if key, insNext := s.treeByID.Ceiling(nextID); key != nil && key.hasSameCreatedAt(nextID) {
					insNext.SetInsPrev(newNode)
				}
				recreated = append(recreated, newNode)
				chainAnchor = newNode
				cursor = gapEnd
			}
		}
	}

	// isolateRange splits of tombstones buffer born-dead remainders into
	// pendingGCPairs. Return them all — including any split-born target that
	// is now un-tombstoned. The caller registers these first, then
	// un-registers the un-tombstoned targets, so the split's GCOnlySize is
	// balanced against the original tombstone's accounting (a bare
	// un-register on a never-registered split piece would leak GC size).
	stillTombstoned = s.drainPendingGCPairs()
	return untombstoned, recreated, stillTombstoned, nil
}

// retombstone re-removes the characters in spans under their original
// identities. Only live pieces are affected; already-removed or purged
// regions are skipped (idempotent). Returns GC pairs for the newly
// tombstoned nodes followed by the born-dead remainders its splits left
// behind, whether any live piece was tombstoned (the rest of the pairs are
// bookkeeping, not an observable change), the net docSize diff from
// splitting live pieces (which the caller accounts to Live), and the pieces
// the removal was recorded on.
func (s *RGATreeSplit[V]) retombstone(
	spans []restoreSpanValue[V],
	executedAt *time.Ticket,
	versionVector time.VersionVector,
) ([]GCPair, bool, resource.DataSize, []restoreSpanValue[V], error) {
	var pairs []GCPair
	var diff resource.DataSize
	var recorded []restoreSpanValue[V]
	record := func(span restoreSpanValue[V], target *RGATreeSplitNode[V]) {
		start := target.ID().Offset()
		end := start + target.contentLen()
		recorded = append(recorded, restoreSpanValue[V]{
			createdAt: span.createdAt,
			start:     start,
			end:       end,
			value:     subValue(span.value, start-span.start, end-span.start),
		})
	}

	for _, span := range spans {
		pieces := s.findPiecesOverlapping(span.createdAt, span.start, span.end)
		for _, piece := range pieces {
			if piece.removedAt != nil {
				// A piece live in the redo's view but removed here by a
				// deletion the redo never saw still records the redo.
				if len(versionVector) > 0 && !piece.isRemovedInView(versionVector) {
					pieceStart := piece.ID().Offset()
					pieceEnd := pieceStart + piece.contentLen()
					target, _, err := s.isolateRange(
						piece, max(pieceStart, span.start), min(pieceEnd, span.end))
					if err != nil {
						return nil, false, diff, nil, err
					}
					target.recordRemoval(executedAt)
					record(span, target)
				}
				continue
			}
			// A piece live here but removed in the redo's view (by a deletion
			// whose undo the redo never saw) is left alone, as the redoing
			// replica left it: like a deletion, a redo removes only what was
			// live in its view.
			if len(versionVector) > 0 && piece.isRemovedInView(versionVector) {
				continue
			}
			pieceStart := piece.ID().Offset()
			pieceEnd := pieceStart + piece.contentLen()
			target, splitDiff, err := s.isolateRange(
				piece, max(pieceStart, span.start), min(pieceEnd, span.end))
			if err != nil {
				return nil, false, diff, nil, err
			}
			diff.Add(splitDiff)
			target.recordRemoval(executedAt)
			record(span, target)
			s.treeByIndex.Splay(target.indexNode)
			pairs = append(pairs, GCPair{Parent: s, Child: target})
		}
	}

	// Isolating a range inside an already-tombstoned piece (and splitting the
	// removed attributes of a live one) buffers born-dead remainders into
	// pendingGCPairs. Hand them to the caller with the rest: unregistered,
	// they would never be purged and their size would stay uncharged.
	tombstoned := len(pairs) > 0
	pairs = append(pairs, s.drainPendingGCPairs()...)
	return pairs, tombstoned, diff, recorded, nil
}

// findPiecesOverlapping collects existing nodes (live or tombstoned) of
// insertion createdAt overlapping [start, end), in ascending offset order.
func (s *RGATreeSplit[V]) findPiecesOverlapping(
	createdAt *time.Ticket, start, end int,
) []*RGATreeSplitNode[V] {
	var pieces []*RGATreeSplitNode[V]
	probe := end - 1
	for probe >= 0 {
		id := NewRGATreeSplitNodeID(createdAt, probe)
		key, node := s.treeByID.Floor(id)
		if key == nil || !key.hasSameCreatedAt(id) {
			break
		}
		nodeStart := node.ID().Offset()
		nodeEnd := nodeStart + node.contentLen()
		if nodeEnd <= start {
			break
		}
		if nodeStart < end && nodeEnd > start {
			pieces = append(pieces, node)
		}
		if nodeStart <= start {
			break
		}
		probe = nodeStart - 1
	}
	for i, j := 0, len(pieces)-1; i < j; i, j = i+1, j-1 {
		pieces[i], pieces[j] = pieces[j], pieces[i]
	}
	return pieces
}

// findPieceCovering returns the node of insertion createdAt whose range
// covers offset, if present.
func (s *RGATreeSplit[V]) findPieceCovering(
	createdAt *time.Ticket, offset int,
) *RGATreeSplitNode[V] {
	id := NewRGATreeSplitNodeID(createdAt, offset)
	key, node := s.treeByID.Floor(id)
	if key == nil || !key.hasSameCreatedAt(id) {
		return nil
	}
	nodeStart := node.ID().Offset()
	if nodeStart <= offset && offset < nodeStart+node.contentLen() {
		return node
	}
	return nil
}

// isolateRange splits piece so a node exactly covering [from, to) exists,
// and returns it plus the net docSize diff produced by the splits.
// Requires pieceStart <= from < to <= pieceEnd. A caller that violates that
// gets splitNode's out-of-range error rather than the nil node splitNode
// returns alongside it.
func (s *RGATreeSplit[V]) isolateRange(
	piece *RGATreeSplitNode[V], from, to int,
) (*RGATreeSplitNode[V], resource.DataSize, error) {
	var diff resource.DataSize
	node := piece
	if from > node.ID().Offset() {
		right, d, err := s.splitNode(node, from-node.ID().Offset())
		if err != nil {
			return nil, diff, err
		}
		// splitNode hands back node.next when the offset lands exactly on the
		// end of the node, which the caller's contract (from < to <= pieceEnd)
		// rules out. Guard anyway so a broken contract surfaces as an error
		// instead of a nil dereference below.
		if right == nil {
			return nil, diff, fmt.Errorf("the node to isolate should be found: %s", s.ToTestString())
		}
		diff.Add(d)
		node = right
	}
	newStart := node.ID().Offset()
	if to < newStart+node.contentLen() {
		_, d, err := s.splitNode(node, to-newStart)
		if err != nil {
			return nil, diff, err
		}
		diff.Add(d)
	}
	return node, diff, nil
}

// findRestoreAnchor returns the node to insert a recreated fragment
// [gapStart, gapEnd) of insertion createdAt AFTER. Every rule keys on
// identity (the insertion's own surviving pieces) so all replicas choose
// the same insertion ancestry regardless of GC state:
//
//	(a) piece covering gapEnd → before it (exact original slot)
//	(b) nearest surviving piece left of gapStart → after it
//	(c) nearest surviving piece at or after gapEnd → before it
//	(d) chain anchor: the fragment placed just before this one in the same
//	    restore (document order) → after it, so a purged multi-fragment run is
//	    rebuilt left-to-right rather than reversed
//	(e) the operation's from position (refined) → the surviving left neighbour
//	    of the run, anchoring the first fragment when its whole insertion is
//	    purged; mirrors the JS fallbackAnchor (see design-doc caveat)
//	(f) head (deterministic last resort)
func (s *RGATreeSplit[V]) findRestoreAnchor(
	createdAt *time.Ticket, gapStart, gapEnd int,
	chainAnchor *RGATreeSplitNode[V],
	fromPos *RGATreeSplitNodePos, executedAt *time.Ticket,
) *RGATreeSplitNode[V] {
	if succ := s.findPieceCovering(createdAt, gapEnd); succ != nil {
		return succ.prev
	}
	if gapStart > 0 {
		id := NewRGATreeSplitNodeID(createdAt, gapStart-1)
		if key, node := s.treeByID.Floor(id); key != nil && key.hasSameCreatedAt(id) {
			return node
		}
	}
	// The nearest surviving piece of the insertion after the gap, not the
	// rightmost one: anchoring before a later piece would place the gap
	// after the pieces in between, and a deletion that later spans from the
	// gap to one of them would run from its start to the end of the text.
	afterID := NewRGATreeSplitNodeID(createdAt, gapEnd)
	if key, node := s.treeByID.Ceiling(afterID); key != nil && key.hasSameCreatedAt(afterID) {
		return node.prev
	}
	if chainAnchor != nil {
		return chainAnchor
	}
	// The operation's from position is normalized -- an absolute offset from
	// the head -- so it must be remapped onto the current chain before it can
	// name a node, exactly as JS does (findRestoreAnchor's fallbackAnchor).
	// Without the remap, findNodeWithSplit rejects every offset past the
	// head's own length and this rung never fires.
	if fromPos != nil && executedAt != nil {
		if refined, err := s.refinePos(fromPos); err == nil {
			if left, _, _, err := s.findNodeWithSplit(refined, executedAt); err == nil && left != nil {
				return left
			}
		}
	}
	return s.initialHead
}

func (s *RGATreeSplit[V]) nodes() []*RGATreeSplitNode[V] {
	var nodes []*RGATreeSplitNode[V]

	node := s.initialHead.next
	for node != nil {
		nodes = append(nodes, node)
		node = node.next
	}

	return nodes
}

// ToTestString returns a String containing the metadata of the nodes
// for debugging purpose.
func (s *RGATreeSplit[V]) ToTestString() string {
	builder := strings.Builder{}

	node := s.initialHead
	for node != nil {
		if node.removedAt != nil {
			fmt.Fprintf(&builder, "{%s}", node.toTestString())
		} else {
			fmt.Fprintf(&builder, "[%s]", node.toTestString())
		}
		node = node.next
	}

	return builder.String()
}

// PurgeBarrierAt implements GCBarrier[GCChild]. findNodeWithSplit skips forward
// while node.next was created after the incoming edit, so a tombstone whose
// createdAt precedes the edit stops that walk. Unlinking it hands the next node
// the stopping decision, which is only the same decision once that node is
// causally stable.
func (s *RGATreeSplit[V]) PurgeBarrierAt(child GCChild) *time.Ticket {
	node, ok := child.(*RGATreeSplitNode[V])
	if !ok || node.next == nil {
		return nil
	}
	return node.next.createdAt()
}

func (s *RGATreeSplit[V]) Purge(child GCChild) error {
	node := child.(*RGATreeSplitNode[V])
	if node.skelLen > 0 {
		return nil
	}
	if s.cancelled != nil {
		delete(s.cancelled, node)
	}

	// Keep the node as a skeleton instead of unlinking it. Insertion origins
	// name nodes by ID, and integrate's window is "the nodes between the two
	// origins"; both have to look the same on a replica that collected this
	// tombstone and on one that did not, or the two place a concurrent insert
	// differently. Only the content and attributes go. The attribute GC
	// pairs, if any, keep pointing at the old value, so they still purge.
	node.skelLen = node.contentLen()
	node.value = s.skeletonValue()
	// Nothing reads a skeleton's removal time (removal, canStyle, restore and
	// GC all branch on skelLen first), so every skeleton shares one stand-in
	// ticket. A snapshot then needs no removed_at for it, and a replica that
	// loaded the skeleton from a snapshot holds the same state as the one
	// that collected it. The removal ledger goes with it.
	node.SetRemovedAt(time.InitialTicket)
	s.mergeSkeletons(node)

	return nil
}

// skeletonValue returns the empty value skeletons share.
func (s *RGATreeSplit[V]) skeletonValue() V {
	if !s.hasSkelValue {
		if v, ok := any(s.initialHead.value).(interface{ skeletonValue() RGATreeSplitValue }); ok {
			s.skelValue = v.skeletonValue().(V)
		} else {
			s.skelValue = s.initialHead.value.DeepCopy().(V)
		}
		s.hasSkelValue = true
	}
	return s.skelValue
}

// mergeSkeletons folds a skeleton into adjacent skeletons that continue the
// same insertion (same createdAt, contiguous offsets, adjacent in the list).
// Pieces of one insertion are split on demand and their fragmentation
// already differs between replicas, so placement cannot depend on it; the
// merged node keeps the leftmost piece's ID and origin, and a later lookup
// inside it splits it again.
func (s *RGATreeSplit[V]) mergeSkeletons(node *RGATreeSplitNode[V]) {
	if p := node.prev; p != nil && canMergeSkeletons(p, node) {
		s.absorbSkeleton(p, node)
		node = p
	}
	if n := node.next; n != nil && canMergeSkeletons(node, n) {
		s.absorbSkeleton(node, n)
	}
}

func canMergeSkeletons[V RGATreeSplitValue](a, b *RGATreeSplitNode[V]) bool {
	return a.skelLen > 0 && b.skelLen > 0 && a.next == b && b.insPrev == a &&
		a.id.createdAt.Compare(b.id.createdAt) == 0 &&
		a.id.offset+a.skelLen == b.id.offset
}

// absorbSkeleton merges b (a.next) into a.
func (s *RGATreeSplit[V]) absorbSkeleton(a, b *RGATreeSplitNode[V]) {
	a.skelLen += b.skelLen

	s.treeByIndex.Delete(b.indexNode)
	s.treeByID.Remove(b.id)

	a.next = b.next
	if b.next != nil {
		b.next.prev = a
	}
	b.prev, b.next = nil, nil

	a.insNext = b.insNext
	if b.insNext != nil {
		b.insNext.insPrev = a
	}
	b.insPrev, b.insNext = nil, nil
}

// pieceKey is an allocation-free map key for a piece's start ID.
type pieceKey struct {
	lamport   int64
	delimiter uint32
	actor     time.ActorID
	offset    int
}

func pieceKeyOf(id *RGATreeSplitNodeID) pieceKey {
	return pieceKey{
		lamport:   id.createdAt.Lamport(),
		delimiter: id.createdAt.Delimiter(),
		actor:     id.createdAt.ActorID(),
		offset:    id.offset,
	}
}

// pieceKeyOf0 is pieceKeyOf that maps nil to a key no piece has.
func pieceKeyOf0(id *RGATreeSplitNodeID) pieceKey {
	if id == nil {
		return pieceKey{offset: -1}
	}
	return pieceKeyOf(id)
}
