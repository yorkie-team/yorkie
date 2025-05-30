package crdt

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/yorkie-team/yorkie/pkg/document/time"
	"github.com/yorkie-team/yorkie/pkg/llrb"
	"github.com/yorkie-team/yorkie/pkg/resource"
	"github.com/yorkie-team/yorkie/pkg/splay"
)

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

// Equal returns whether given ID equals to this ID or not.
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
func (s *RGATreeSplitNode[V]) Remove(removedAt *time.Ticket, clientLamportAtChange int64) bool {
	justRemoved := s.removedAt == nil
	nodeExisted := s.createdAt().Lamport() <= clientLamportAtChange

	if nodeExisted &&
		(s.removedAt == nil || removedAt.After(s.removedAt)) {
		s.removedAt = removedAt
		return justRemoved
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
	splayNode, offset, err := s.treeByIndex.Find(index)
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
		return nil, fmt.Errorf("the node of the given id should be found: " + s.ToTestString())
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
		return nil, diff, fmt.Errorf("offset should be less than or equal to length: " + s.ToTestString())
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

	return splitNode, diff, nil
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
		return nil, nil, diff, err
	}

	fromLeft, fromRight, diffFrom, err := s.findNodeWithSplit(from, editedAt)
	if err != nil {
		return nil, nil, diff, err
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
	removedNodeMap := make(map[string]*RGATreeSplitNode[V])
	isVersionVectorEmpty := len(vector) == 0

	if len(candidates) == 0 {
		return removedNodeMap
	}

	// There are 2 types of nodes in `candidates`: should delete, should not delete.
	// `nodesToKeep` contains nodes should not delete,
	// then is used to find the boundary of the range to be deleted.
	var nodesToKeep []*RGATreeSplitNode[V]
	leftEdge, rightEdge := s.findEdgesOfCandidates(candidates)
	nodesToKeep = append(nodesToKeep, leftEdge)

	for _, node := range candidates {
		actorID := node.createdAt().ActorID()

		var clientLamportAtChange int64
		if isVersionVectorEmpty {
			// Case 1: local editing from json package
			clientLamportAtChange = time.MaxLamport
		} else {
			// Case 2: from operation with version vector(After v0.5.7)
			lamport, ok := vector.Get(actorID)
			if ok {
				clientLamportAtChange = lamport
			} else {
				clientLamportAtChange = 0
			}
		}

		if node.Remove(editedAt, clientLamportAtChange) {
			removedNodeMap[node.id.key()] = node
		} else {
			nodesToKeep = append(nodesToKeep, node)
		}
	}
	nodesToKeep = append(nodesToKeep, rightEdge)
	s.deleteIndexNodes(nodesToKeep)

	return removedNodeMap
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
