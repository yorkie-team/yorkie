package crdt

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/yorkie-team/yorkie/pkg/document/resource"
	"github.com/yorkie-team/yorkie/pkg/document/time"
	"github.com/yorkie-team/yorkie/pkg/llrb"
	"github.com/yorkie-team/yorkie/pkg/splay"
)

// RestoreMode selects the identity-preserving path for undo/redo of a
// Text deletion.
type RestoreMode int

const (
	// RestoreModeNone means an ordinary edit (no restore semantics).
	RestoreModeNone RestoreMode = iota
	// RestoreModeRestore re-establishes removed characters under their
	// original identities (undo of a deletion).
	RestoreModeRestore
	// RestoreModeRetombstone re-deletes previously restored characters
	// (redo of an identity-preserving undo).
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

var (
	initialNodeID = NewRGATreeSplitNodeID(time.InitialTicket, 0)
)

// RGATreeSplitValue is a value of RGATreeSplitNode.
type RGATreeSplitValue interface {
	Split(offset int) RGATreeSplitValue
	Len() int
	DeepCopy() RGATreeSplitValue
	String() string
	Marshal() string
	toTestString() string
	DataSize() resource.DataSize
}

// RGATreeSplitNodeID is an ID of RGATreeSplitNode.
type RGATreeSplitNodeID struct {
	createdAt *time.Ticket
	offset    int

	// cachedKey is the cache of the string representation of the ID.
	cachedKey string
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

// key returns a string representation of the ID. The result will be
// cached in the key field to prevent instantiation of a new string.
func (id *RGATreeSplitNodeID) key() string {
	if id.cachedKey == "" {
		id.cachedKey = id.createdAt.Key() + ":" + strconv.FormatUint(uint64(id.offset), 10)
	}

	return id.cachedKey
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

	prev    *RGATreeSplitNode[V]
	next    *RGATreeSplitNode[V]
	insPrev *RGATreeSplitNode[V]
	insNext *RGATreeSplitNode[V]
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

// InsPrevID returns previous node ID at the time of this node insertion.
func (s *RGATreeSplitNode[V]) InsPrevID() *RGATreeSplitNodeID {
	if s.insPrev == nil {
		return nil
	}

	return s.insPrev.id
}

func (s *RGATreeSplitNode[V]) contentLen() int {
	return s.value.Len()
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

// SetRemovedAt sets the removal time of this node.
func (s *RGATreeSplitNode[V]) SetRemovedAt(removedAt *time.Ticket) {
	s.removedAt = removedAt
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
	}
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
	newNode := NewRGATreeSplitNode(
		s.id.Split(offset),
		s.value.Split(offset).(V),
	)
	newNode.removedAt = s.removedAt
	return newNode
}

func (s *RGATreeSplitNode[V]) createdAt() *time.Ticket {
	return s.id.createdAt
}

// toTestString returns a String containing the metadata of the node
// for debugging purpose.
func (s *RGATreeSplitNode[V]) toTestString() string {
	return fmt.Sprintf("%s %s", s.id.ToTestString(), s.value.toTestString())
}

// Remove removes this node if it created before the time of deletion are
// deleted. It only marks the deleted time (tombstone).
func (s *RGATreeSplitNode[V]) Remove(removedAt *time.Ticket, creationKnown bool, tombstoneKnown bool) bool {
	// NOTE(hackerwins): Skip if the node's creation was not visible to this
	// operation.
	if !creationKnown {
		return false
	}

	if s.removedAt == nil {
		s.removedAt = removedAt
		return true
	}

	// NOTE(hackerwins): Overwrite only if prior tombstone was not known
	// (concurrent or unseen) and newer.
	if !tombstoneKnown && removedAt.After(s.removedAt) {
		s.removedAt = removedAt
	}
	return false
}

// canStyle checks if node is able to set style.
func (s *RGATreeSplitNode[V]) canStyle(editedAt *time.Ticket, clientLamportAtChange int64) bool {
	nodeExisted := s.createdAt().Lamport() <= clientLamportAtChange

	return nodeExisted &&
		(s.removedAt == nil || editedAt.After(s.removedAt))
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
	absoluteID := pos.getAbsoluteID()
	node, err := s.findFloorNodePreferToLeft(absoluteID)
	if err != nil {
		return nil, nil, resource.DataSize{}, err
	}

	relativeOffset := absoluteID.offset - node.id.offset

	_, diff, err := s.splitNode(node, relativeOffset)
	if err != nil {
		return nil, nil, resource.DataSize{}, err
	}

	for node.next != nil && node.next.createdAt().After(updatedAt) {
		node = node.next
	}

	return node, node.next, diff, nil
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

func (s *RGATreeSplit[V]) splitNode(
	node *RGATreeSplitNode[V],
	offset int,
) (*RGATreeSplitNode[V], resource.DataSize, error) {
	var diff resource.DataSize

	if offset > node.contentLen() {
		return nil, diff, fmt.Errorf("offset should be less than or equal to length: %s", s.ToTestString())
	}

	if offset == 0 {
		return node, diff, nil
	} else if offset == node.contentLen() {
		return node.next, diff, nil
	}

	prevSize := node.DataSize()

	splitNode := node.split(offset)
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

func (s *RGATreeSplit[V]) edit(
	from *RGATreeSplitNodePos,
	to *RGATreeSplitNodePos,
	content V,
	editedAt *time.Ticket,
	versionVector time.VersionVector,
) (*RGATreeSplitNodePos, []GCPair, resource.DataSize, error) {
	var diff resource.DataSize

	// 01. Split nodes with from and to
	toLeft, toRight, diffTo, err := s.findNodeWithSplit(to, editedAt)
	if err != nil {
		return nil, s.drainPendingGCPairs(), diff, err
	}

	fromLeft, fromRight, diffFrom, err := s.findNodeWithSplit(from, editedAt)
	if err != nil {
		diff.Add(diffTo)
		return nil, s.drainPendingGCPairs(), diff, err
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
		inserted := s.InsertAfter(fromLeft, NewRGATreeSplitNode(NewRGATreeSplitNodeID(editedAt, 0), content))
		diff.Add(inserted.DataSize())

		caretPos = NewRGATreeSplitNodePos(inserted.id, inserted.contentLen())
	}

	// 04. add removed node
	var pairs []GCPair
	for _, removedNode := range removedNodes {
		pairs = append(pairs, GCPair{
			Parent: s,
			Child:  removedNode,
		})
	}

	pairs = append(pairs, s.drainPendingGCPairs()...)

	return caretPos, pairs, diff, nil
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

		if n.Remove(editedAt, creationKnown, tombstoneKnown) {
			removed[n.id.key()] = n
		} else {
			nodesToKeep = append(nodesToKeep, n)
		}
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
//   - live piece exists       → skip (idempotent; another undo restored it)
//   - tombstoned piece exists → clear removedAt (un-tombstone)
//   - no piece exists (GC'd)  → recreate a node with the original ID
//
// Returns (untombstoned, recreated, stillTombstoned):
//   - untombstoned: nodes flipped tombstone→live (caller GC-unregisters the
//     pre-existing ones and moves their size GC→Live)
//   - recreated: brand-new live nodes for purged regions (caller adds their
//     size to Live)
//   - stillTombstoned: born-tombstoned split remainders that must be
//     GC-registered (drained pendingGCPairs, minus un-tombstoned targets)
func (s *RGATreeSplit[V]) restore(
	spans []restoreSpanValue[V],
	executedAt *time.Ticket,
	fallbackAnchor *RGATreeSplitNodePos,
) (untombstoned, recreated []*RGATreeSplitNode[V], stillTombstoned []GCPair) {
	targets := map[string]struct{}{}

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
				if piece.removedAt != nil {
					target, _ := s.isolateRange(piece, cursor, overlapEnd)
					target.SetRemovedAt(nil)
					s.treeByIndex.Splay(target.indexNode)
					untombstoned = append(untombstoned, target)
					targets[target.IDString()] = struct{}{}
				}
				cursor = overlapEnd
				if overlapEnd >= pieceEnd {
					pieceIdx++
				}
			} else {
				gapEnd := min(pieceStart, span.end)
				val := subValue(span.value, cursor-span.start, gapEnd-span.start)
				newNode := NewRGATreeSplitNode(
					NewRGATreeSplitNodeID(span.createdAt, cursor), val)
				prev := s.findRestoreAnchor(
					span.createdAt, cursor, gapEnd, executedAt, fallbackAnchor)
				s.InsertAfter(prev, newNode)
				recreated = append(recreated, newNode)
				cursor = gapEnd
			}
		}
	}

	// isolateRange splits of tombstones buffer born-dead remainders into
	// pendingGCPairs. An un-tombstoned target that was itself split-born is
	// in that buffer but is now live — drop it; register the rest.
	for _, pair := range s.drainPendingGCPairs() {
		if _, isTarget := targets[pair.Child.IDString()]; !isTarget {
			stillTombstoned = append(stillTombstoned, pair)
		}
	}
	return untombstoned, recreated, stillTombstoned
}

// retombstone re-deletes the characters in spans (redo of an
// identity-preserving undo). Only live pieces are affected; already-removed
// or purged regions are skipped (idempotent). Returns GC pairs for the
// newly tombstoned nodes and the net docSize diff from splitting live
// pieces (which the caller accounts to Live).
func (s *RGATreeSplit[V]) retombstone(
	spans []restoreSpanValue[V],
	executedAt *time.Ticket,
) ([]GCPair, resource.DataSize) {
	var pairs []GCPair
	var diff resource.DataSize

	for _, span := range spans {
		pieces := s.findPiecesOverlapping(span.createdAt, span.start, span.end)
		for _, piece := range pieces {
			if piece.removedAt != nil {
				continue
			}
			pieceStart := piece.ID().Offset()
			pieceEnd := pieceStart + piece.contentLen()
			target, splitDiff := s.isolateRange(
				piece, max(pieceStart, span.start), min(pieceEnd, span.end))
			diff.Add(splitDiff)
			target.SetRemovedAt(executedAt)
			s.treeByIndex.Splay(target.indexNode)
			pairs = append(pairs, GCPair{Parent: s, Child: target})
		}
	}
	return pairs, diff
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
// Requires pieceStart <= from < to <= pieceEnd.
func (s *RGATreeSplit[V]) isolateRange(
	piece *RGATreeSplitNode[V], from, to int,
) (*RGATreeSplitNode[V], resource.DataSize) {
	var diff resource.DataSize
	node := piece
	if from > node.ID().Offset() {
		right, d, _ := s.splitNode(node, from-node.ID().Offset())
		diff.Add(d)
		node = right
	}
	newStart := node.ID().Offset()
	if to < newStart+node.contentLen() {
		_, d, _ := s.splitNode(node, to-newStart)
		diff.Add(d)
	}
	return node, diff
}

// findRestoreAnchor returns the node to insert a recreated fragment
// [gapStart, gapEnd) of insertion createdAt AFTER. Resolution ladder:
//
//	(a) piece covering gapEnd → before it (exact original slot)
//	(b) nearest surviving piece left of gapStart → after it
//	(c) rightmost surviving piece (right of gap) → before it
//	(d) op fallback anchor (refined) — the one non-replica-invariant rule
//	(e) head (deterministic last resort)
func (s *RGATreeSplit[V]) findRestoreAnchor(
	createdAt *time.Ticket, gapStart, gapEnd int,
	executedAt *time.Ticket, fallback *RGATreeSplitNodePos,
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
	rightmostID := NewRGATreeSplitNodeID(createdAt, math.MaxInt32)
	if key, node := s.treeByID.Floor(rightmostID); key != nil &&
		key.hasSameCreatedAt(rightmostID) && node.ID().Offset() >= gapEnd {
		return node.prev
	}
	if fallback != nil {
		if left, _, _, err := s.findNodeWithSplit(fallback, executedAt); err == nil {
			return left
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
			builder.WriteString(fmt.Sprintf("{%s}", node.toTestString()))
		} else {
			builder.WriteString(fmt.Sprintf("[%s]", node.toTestString()))
		}
		node = node.next
	}

	return builder.String()
}

// Purge physically purge the given node from RGATreeSplit.
func (s *RGATreeSplit[V]) Purge(child GCChild) error {
	node := child.(*RGATreeSplitNode[V])

	s.treeByIndex.Delete(node.indexNode)
	s.treeByID.Remove(node.id)

	node.prev.next = node.next
	if node.next != nil {
		node.next.prev = node.prev
	}
	node.prev, node.next = nil, nil

	if node.insPrev != nil {
		node.insPrev.insNext = node.insNext
	}
	if node.insNext != nil {
		node.insNext.insPrev = node.insPrev
	}
	node.insPrev, node.insNext = nil, nil

	return nil
}
