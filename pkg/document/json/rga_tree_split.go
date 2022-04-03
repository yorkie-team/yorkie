package json

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/yorkie-team/yorkie/pkg/document/time"
	"github.com/yorkie-team/yorkie/pkg/llrb"
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
	AnnotatedString() string
}

// RGATreeSplitNodeID is an ID of RGATreeSplitNode.
type RGATreeSplitNodeID struct {
	createdAt *time.Ticket
	offset    int
	key       string
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
// argument is nil, it would panic at runtime.
func (t *RGATreeSplitNodeID) Compare(other llrb.Key) int {
	if t == nil || other == nil {
		panic("RGATreeSplitNodeID cannot be null")
	}

	o := other.(*RGATreeSplitNodeID)
	compare := t.createdAt.Compare(o.createdAt)
	if compare != 0 {
		return compare
	}

	if t.offset > o.offset {
		return 1
	} else if t.offset < o.offset {
		return -1
	}

	return 0
}

// Equal returns whether given ID equals to this ID or not.
func (t *RGATreeSplitNodeID) Equal(other llrb.Key) bool {
	return t.Compare(other) == 0
}

// CreatedAt returns the creation time of this ID.
func (t *RGATreeSplitNodeID) CreatedAt() *time.Ticket {
	return t.createdAt
}

// Offset returns the offset of this ID.
func (t *RGATreeSplitNodeID) Offset() int {
	return t.offset
}

// Split creates a new ID with an offset from this ID.
func (t *RGATreeSplitNodeID) Split(offset int) *RGATreeSplitNodeID {
	return NewRGATreeSplitNodeID(t.createdAt, t.offset+offset)
}

// AnnotatedString returns a String containing the metadata of the node id
// for debugging purpose.
func (t *RGATreeSplitNodeID) AnnotatedString() string {
	return fmt.Sprintf("%s:%d", t.createdAt.AnnotatedString(), t.offset)
}

func (t *RGATreeSplitNodeID) hasSameCreatedAt(id *RGATreeSplitNodeID) bool {
	return t.createdAt.Compare(id.createdAt) == 0
}

// Key returns a string representation of the ID. The result will be
// cached in the key field to prevent instantiation of a new string.
func (t *RGATreeSplitNodeID) Key() string {
	if t.key == "" {
		t.key = t.createdAt.Key() + ":" + strconv.FormatUint(uint64(t.offset), 10)
	}

	return t.key
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

// AnnotatedString returns a String containing the metadata of the position
// for debugging purpose.
func (pos *RGATreeSplitNodePos) AnnotatedString() string {
	return fmt.Sprintf("%s:%d", pos.id.AnnotatedString(), pos.relativeOffset)
}

// ID returns the ID of this RGATreeSplitNodePos.
func (pos *RGATreeSplitNodePos) ID() *RGATreeSplitNodeID {
	return pos.id
}

// RelativeOffset returns the relative offset of this RGATreeSplitNodePos.
func (pos *RGATreeSplitNodePos) RelativeOffset() int {
	return pos.relativeOffset
}

// Equal returns the whether the given pos equals or not.
func (pos *RGATreeSplitNodePos) Equal(other *RGATreeSplitNodePos) bool {
	if !pos.id.Equal(other.id) {
		return false
	}
	return pos.relativeOffset == other.relativeOffset
}

// Selection represents the selection of text range in the editor.
type Selection struct {
	from      *RGATreeSplitNodePos
	to        *RGATreeSplitNodePos
	updatedAt *time.Ticket
}

func newSelection(from, to *RGATreeSplitNodePos, updatedAt *time.Ticket) *Selection {
	return &Selection{
		from,
		to,
		updatedAt,
	}
}

// RGATreeSplitNode is a node of RGATreeSplit.
type RGATreeSplitNode struct {
	id        *RGATreeSplitNodeID
	indexNode *splay.Node
	value     RGATreeSplitValue
	removedAt *time.Ticket

	prev    *RGATreeSplitNode
	next    *RGATreeSplitNode
	insPrev *RGATreeSplitNode
	insNext *RGATreeSplitNode
}

// NewRGATreeSplitNode creates a new instance of RGATreeSplit.
func NewRGATreeSplitNode(id *RGATreeSplitNodeID, value RGATreeSplitValue) *RGATreeSplitNode {
	node := &RGATreeSplitNode{
		id:    id,
		value: value,
	}
	node.indexNode = splay.NewNode(node)

	return node
}

// ID returns the ID of this RGATreeSplitNode.
func (t *RGATreeSplitNode) ID() *RGATreeSplitNodeID {
	return t.id
}

// InsPrevID returns previous node ID at the time of this node insertion.
func (t *RGATreeSplitNode) InsPrevID() *RGATreeSplitNodeID {
	if t.insPrev == nil {
		return nil
	}

	return t.insPrev.id
}

func (t *RGATreeSplitNode) contentLen() int {
	return t.value.Len()
}

// Len returns the length of this node.
func (t *RGATreeSplitNode) Len() int {
	if t.removedAt != nil {
		return 0
	}
	return t.contentLen()
}

// RemovedAt return the remove time of this node.
func (t *RGATreeSplitNode) RemovedAt() *time.Ticket {
	return t.removedAt
}

// String returns the string representation of this node.
func (t *RGATreeSplitNode) String() string {
	return t.value.String()
}

// DeepCopy returns a new instance of this RGATreeSplitNode without structural info.
func (t *RGATreeSplitNode) DeepCopy() *RGATreeSplitNode {
	node := &RGATreeSplitNode{
		id:        t.id,
		value:     t.value.DeepCopy(),
		removedAt: t.removedAt,
	}
	node.indexNode = splay.NewNode(node)

	return node
}

// SetInsPrev sets previous node of this node insertion.
func (t *RGATreeSplitNode) SetInsPrev(node *RGATreeSplitNode) {
	t.insPrev = node
	node.insNext = t
}

func (t *RGATreeSplitNode) setPrev(node *RGATreeSplitNode) {
	t.prev = node
	node.next = t
}

func (t *RGATreeSplitNode) split(offset int) *RGATreeSplitNode {
	return NewRGATreeSplitNode(
		t.id.Split(offset),
		t.value.Split(offset),
	)
}

func (t *RGATreeSplitNode) createdAt() *time.Ticket {
	return t.id.createdAt
}

// annotatedString returns a String containing the metadata of the node
// for debugging purpose.
func (t *RGATreeSplitNode) annotatedString() string {
	return fmt.Sprintf("%s %s", t.id.AnnotatedString(), t.value.AnnotatedString())
}

// Remove removes this node if it created before the time of deletion are
// deleted. It only marks the deleted time (tombstone).
func (t *RGATreeSplitNode) Remove(removedAt *time.Ticket, latestCreatedAt *time.Ticket) bool {
	if !t.createdAt().After(latestCreatedAt) &&
		(t.removedAt == nil || removedAt.After(t.removedAt)) {
		t.removedAt = removedAt
		return true
	}
	return false
}

// Value returns the value of this node.
func (t *RGATreeSplitNode) Value() RGATreeSplitValue {
	return t.value
}

// RGATreeSplit is a block-based list with improved index-based lookup in RGA.
// The difference from RGATreeList is that it has data on a block basis to
// reduce the size of CRDT metadata. When an edit occurs on a block,
// the block is split.
type RGATreeSplit struct {
	initialHead *RGATreeSplitNode
	treeByIndex *splay.Tree
	treeByID    *llrb.Tree

	// removedNodeMap is a map that holds tombstone nodes
	// when the edit operation is executed.
	removedNodeMap map[string]*RGATreeSplitNode
}

// NewRGATreeSplit creates a new instance of RGATreeSplit.
func NewRGATreeSplit(initialHead *RGATreeSplitNode) *RGATreeSplit {
	treeByIndex := splay.NewTree(initialHead.indexNode)
	treeByID := llrb.NewTree()
	treeByID.Put(initialHead.ID(), initialHead)

	return &RGATreeSplit{
		initialHead:    initialHead,
		treeByIndex:    treeByIndex,
		treeByID:       treeByID,
		removedNodeMap: make(map[string]*RGATreeSplitNode),
	}
}

func (s *RGATreeSplit) createRange(from, to int) (*RGATreeSplitNodePos, *RGATreeSplitNodePos) {
	fromPos := s.findNodePos(from)
	if from == to {
		return fromPos, fromPos
	}

	return fromPos, s.findNodePos(to)
}

func (s *RGATreeSplit) findNodePos(index int) *RGATreeSplitNodePos {
	splayNode, offset := s.treeByIndex.Find(index)
	node := splayNode.Value().(*RGATreeSplitNode)
	return &RGATreeSplitNodePos{
		id:             node.ID(),
		relativeOffset: offset,
	}
}

func (s *RGATreeSplit) findNodeWithSplit(
	pos *RGATreeSplitNodePos,
	updatedAt *time.Ticket,
) (*RGATreeSplitNode, *RGATreeSplitNode) {
	absoluteID := pos.getAbsoluteID()
	node := s.findFloorNodePreferToLeft(absoluteID)

	relativeOffset := absoluteID.offset - node.id.offset

	s.splitNode(node, relativeOffset)

	for node.next != nil && node.next.createdAt().After(updatedAt) {
		node = node.next
	}

	return node, node.next
}

func (s *RGATreeSplit) findFloorNodePreferToLeft(id *RGATreeSplitNodeID) *RGATreeSplitNode {
	node := s.findFloorNode(id)
	if node == nil {
		panic("the node of the given id should be found: " + s.AnnotatedString())
	}

	if id.offset > 0 && node.id.offset == id.offset {
		// NOTE: InsPrev may not be present due to GC.
		if node.insPrev == nil {
			return node
		}
		node = node.insPrev
	}

	return node
}

func (s *RGATreeSplit) splitNode(node *RGATreeSplitNode, offset int) *RGATreeSplitNode {
	if offset > node.contentLen() {
		panic("offset should be less than or equal to length: " + s.AnnotatedString())
	}

	if offset == 0 {
		return node
	} else if offset == node.contentLen() {
		return node.next
	}

	splitNode := node.split(offset)
	s.treeByIndex.UpdateSubtree(splitNode.indexNode)
	s.InsertAfter(node, splitNode)

	insNext := node.insNext
	if insNext != nil {
		insNext.SetInsPrev(splitNode)
	}
	splitNode.SetInsPrev(node)

	return splitNode
}

// InsertAfter inserts the given node after the given previous node.
func (s *RGATreeSplit) InsertAfter(prev *RGATreeSplitNode, node *RGATreeSplitNode) *RGATreeSplitNode {
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
func (s *RGATreeSplit) InitialHead() *RGATreeSplitNode {
	return s.initialHead
}

// FindNode returns the node of the given ID.
func (s *RGATreeSplit) FindNode(id *RGATreeSplitNodeID) *RGATreeSplitNode {
	if id == nil {
		return nil
	}

	return s.findFloorNode(id)
}

func (s *RGATreeSplit) findFloorNode(id *RGATreeSplitNodeID) *RGATreeSplitNode {
	key, value := s.treeByID.Floor(id)
	if key == nil {
		return nil
	}

	foundID := key.(*RGATreeSplitNodeID)
	foundValue := value.(*RGATreeSplitNode)

	if !foundID.Equal(id) && !foundID.hasSameCreatedAt(id) {
		return nil
	}

	return foundValue
}

func (s *RGATreeSplit) edit(
	from *RGATreeSplitNodePos,
	to *RGATreeSplitNodePos,
	latestCreatedAtMapByActor map[string]*time.Ticket,
	content RGATreeSplitValue,
	editedAt *time.Ticket,
) (*RGATreeSplitNodePos, map[string]*time.Ticket) {
	// 01. Split nodes with from and to
	toLeft, toRight := s.findNodeWithSplit(to, editedAt)
	fromLeft, fromRight := s.findNodeWithSplit(from, editedAt)

	// 02. delete between from and to
	nodesToDelete := s.findBetween(fromRight, toRight)
	latestCreatedAtMap, removedNodeMapByNodeKey := s.deleteNodes(nodesToDelete, latestCreatedAtMapByActor, editedAt)

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
		caretPos = NewRGATreeSplitNodePos(inserted.id, inserted.contentLen())
	}

	// 04. add removed node
	for key, removedNode := range removedNodeMapByNodeKey {
		s.removedNodeMap[key] = removedNode
	}

	return caretPos, latestCreatedAtMap
}

func (s *RGATreeSplit) findBetween(from *RGATreeSplitNode, to *RGATreeSplitNode) []*RGATreeSplitNode {
	current := from
	var nodes []*RGATreeSplitNode
	for current != nil && current != to {
		nodes = append(nodes, current)
		current = current.next
	}
	return nodes
}

func (s *RGATreeSplit) deleteNodes(
	candidates []*RGATreeSplitNode,
	latestCreatedAtMapByActor map[string]*time.Ticket,
	editedAt *time.Ticket,
) (map[string]*time.Ticket, map[string]*RGATreeSplitNode) {
	createdAtMapByActor := make(map[string]*time.Ticket)
	removedNodeMap := make(map[string]*RGATreeSplitNode)

	for _, node := range candidates {
		actorIDHex := node.createdAt().ActorIDHex()

		var latestCreatedAt *time.Ticket
		if latestCreatedAtMapByActor == nil {
			latestCreatedAt = time.MaxTicket
		} else {
			createdAt, ok := latestCreatedAtMapByActor[actorIDHex]
			if ok {
				latestCreatedAt = createdAt
			} else {
				latestCreatedAt = time.InitialTicket
			}
		}

		if node.Remove(editedAt, latestCreatedAt) {
			s.treeByIndex.Splay(node.indexNode)
			latestCreatedAt := createdAtMapByActor[actorIDHex]
			createdAt := node.id.createdAt
			if latestCreatedAt == nil || createdAt.After(latestCreatedAt) {
				createdAtMapByActor[actorIDHex] = createdAt
			}

			removedNodeMap[node.id.Key()] = node
		}
	}

	return createdAtMapByActor, removedNodeMap
}

func (s *RGATreeSplit) marshal() string {
	var values []string

	node := s.initialHead.next
	for node != nil {
		if node.removedAt == nil {
			values = append(values, node.String())
		}
		node = node.next
	}

	return strings.Join(values, "")
}

func (s *RGATreeSplit) nodes() []*RGATreeSplitNode {
	var nodes []*RGATreeSplitNode

	node := s.initialHead.next
	for node != nil {
		nodes = append(nodes, node)
		node = node.next
	}

	return nodes
}

// AnnotatedString returns a String containing the metadata of the nodes
// for debugging purpose.
func (s *RGATreeSplit) AnnotatedString() string {
	var result []string

	node := s.initialHead
	for node != nil {
		if node.removedAt != nil {
			result = append(result, fmt.Sprintf(
				"{%s}",
				node.annotatedString(),
			))
		} else {
			result = append(result, fmt.Sprintf(
				"[%s]",
				node.annotatedString(),
			))
		}

		node = node.next
	}

	return strings.Join(result, "")
}

// removedNodesLen returns length of removed nodes
func (s *RGATreeSplit) removedNodesLen() int {
	return len(s.removedNodeMap)
}

// purgeTextNodesWithGarbage physically purges nodes that have been removed.
func (s *RGATreeSplit) purgeTextNodesWithGarbage(ticket *time.Ticket) int {
	count := 0
	for _, node := range s.removedNodeMap {
		if node.removedAt != nil && ticket.Compare(node.removedAt) >= 0 {
			s.treeByIndex.Delete(node.indexNode)
			s.purge(node)
			s.treeByID.Remove(node.id)
			delete(s.removedNodeMap, node.id.Key())
			count++
		}
	}

	return count
}

// purge physically purge the given node from RGATreeSplit.
func (s *RGATreeSplit) purge(node *RGATreeSplitNode) {
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
}
