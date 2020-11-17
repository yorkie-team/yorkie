package json

import (
	"fmt"
	"strings"

	"github.com/yorkie-team/yorkie/pkg/document/time"
	"github.com/yorkie-team/yorkie/pkg/llrb"
	"github.com/yorkie-team/yorkie/pkg/log"
	"github.com/yorkie-team/yorkie/pkg/splay"
)

var (
	initialNodeID = NewRGATreeSplitNodeID(time.InitialTicket, 0)
)

type RGATreeSplitValue interface {
	Split(offset int) RGATreeSplitValue
	Len() int
	DeepCopy() RGATreeSplitValue
	String() string
	AnnotatedString() string
}

type RGATreeSplitNodeID struct {
	createdAt *time.Ticket
	offset    int
}

func NewRGATreeSplitNodeID(createdAt *time.Ticket, offset int) *RGATreeSplitNodeID {
	return &RGATreeSplitNodeID{
		createdAt: createdAt,
		offset:    offset,
	}
}

func (t *RGATreeSplitNodeID) Compare(other llrb.Key) int {
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

func (t *RGATreeSplitNodeID) Equal(other *RGATreeSplitNodeID) bool {
	return t.Compare(other) == 0
}

func (t *RGATreeSplitNodeID) CreatedAt() *time.Ticket {
	return t.createdAt
}

func (t *RGATreeSplitNodeID) Offset() int {
	return t.offset
}

func (t *RGATreeSplitNodeID) Split(offset int) *RGATreeSplitNodeID {
	return NewRGATreeSplitNodeID(t.createdAt, t.offset+offset)
}

// AnnotatedString returns a String containing the meta data of the node id
// for debugging purpose.
func (t *RGATreeSplitNodeID) AnnotatedString() string {
	return fmt.Sprintf("%s:%d", t.createdAt.AnnotatedString(), t.offset)
}

func (t *RGATreeSplitNodeID) hasSameCreatedAt(id *RGATreeSplitNodeID) bool {
	return t.createdAt.Compare(id.createdAt) == 0
}

type RGATreeSplitNodePos struct {
	id             *RGATreeSplitNodeID
	relativeOffset int
}

func NewRGATreeSplitNodePos(id *RGATreeSplitNodeID, offset int) *RGATreeSplitNodePos {
	return &RGATreeSplitNodePos{id, offset}
}

func (pos *RGATreeSplitNodePos) getAbsoluteID() *RGATreeSplitNodeID {
	return NewRGATreeSplitNodeID(pos.id.createdAt, pos.id.offset+pos.relativeOffset)
}

// AnnotatedString returns a String containing the meta data of the position
// for debugging purpose.
func (pos *RGATreeSplitNodePos) AnnotatedString() string {
	return fmt.Sprintf("%s:%d", pos.id.AnnotatedString(), pos.relativeOffset)
}

func (pos *RGATreeSplitNodePos) ID() *RGATreeSplitNodeID {
	return pos.id
}

func (pos *RGATreeSplitNodePos) RelativeOffset() int {
	return pos.relativeOffset
}

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

func NewRGATreeSplitNode(id *RGATreeSplitNodeID, value RGATreeSplitValue) *RGATreeSplitNode {
	node := &RGATreeSplitNode{
		id:    id,
		value: value,
	}
	node.indexNode = splay.NewNode(node)

	return node
}

func (t *RGATreeSplitNode) ID() *RGATreeSplitNodeID {
	return t.id
}

func (t *RGATreeSplitNode) InsPrevID() *RGATreeSplitNodeID {
	if t.insPrev == nil {
		return nil
	}

	return t.insPrev.id
}

func (t *RGATreeSplitNode) contentLen() int {
	return t.value.Len()
}

func (t *RGATreeSplitNode) Len() int {
	if t.removedAt != nil {
		return 0
	}
	return t.contentLen()
}

func (t *RGATreeSplitNode) RemovedAt() *time.Ticket {
	return t.removedAt
}

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

// annotatedString returns a String containing the meta data of the node
// for debugging purpose.
func (t *RGATreeSplitNode) annotatedString() string {
	return fmt.Sprintf("%s %s", t.id.AnnotatedString(), t.value.AnnotatedString())
}

func (t *RGATreeSplitNode) Remove(removedAt *time.Ticket, latestCreatedAt *time.Ticket) bool {
	if !t.createdAt().After(latestCreatedAt) &&
		(t.removedAt == nil || removedAt.After(t.removedAt)) {
		t.removedAt = removedAt
		return true
	}
	return false
}

func (t *RGATreeSplitNode) Value() RGATreeSplitValue {
	return t.value
}

// removedNodeMapKey is a key of removedNodeMap.
// The shape of the key is '{createdAt.Key()}:{offset}'.
type removedNodeMapKey string

// createRemovedNodeMapKey generates keys for removedNodes.
func createRemovedNodeMapKey(createdAtKey string, offset int) removedNodeMapKey {
	key := fmt.Sprintf("%s:%d", createdAtKey, offset)
	return removedNodeMapKey(key)
}

type RGATreeSplit struct {
	initialHead *RGATreeSplitNode
	treeByIndex *splay.Tree
	treeByID    *llrb.Tree

	removedNodeMap map[removedNodeMapKey]*RGATreeSplitNode
}

func NewRGATreeSplit(initialHead *RGATreeSplitNode) *RGATreeSplit {
	treeByIndex := splay.NewTree(initialHead.indexNode)
	treeByID := llrb.NewTree()
	treeByID.Put(initialHead.ID(), initialHead)

	return &RGATreeSplit{
		initialHead:    initialHead,
		treeByIndex:    treeByIndex,
		treeByID:       treeByID,
		removedNodeMap: make(map[removedNodeMapKey]*RGATreeSplitNode),
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
		log.Logger.Error(s.AnnotatedString())
		panic("the node of the given id should be found")
	}

	if id.offset > 0 && node.id.offset == id.offset {
		if node.insPrev == nil {
			log.Logger.Error(s.AnnotatedString())
			panic("insPrev should be presence")
		}
		node = node.insPrev
	}

	return node
}

func (s *RGATreeSplit) splitNode(node *RGATreeSplitNode, offset int) *RGATreeSplitNode {
	if offset > node.contentLen() {
		log.Logger.Error(s.AnnotatedString())
		panic("offset should be less than or equal to length")
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

func (s *RGATreeSplit) InitialHead() *RGATreeSplitNode {
	return s.initialHead
}

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
	latestCreatedAtMap, removedNodeMapByCreatedAt := s.deleteNodes(nodesToDelete, latestCreatedAtMapByActor, editedAt)

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
	for key, removedNode := range removedNodeMapByCreatedAt {
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
) (map[string]*time.Ticket, map[removedNodeMapKey]*RGATreeSplitNode) {
	createdAtMapByActor := make(map[string]*time.Ticket)
	removedNodeMap := make(map[removedNodeMapKey]*RGATreeSplitNode)

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

			key := createRemovedNodeMapKey(createdAt.Key(), node.id.offset)
			removedNodeMap[key] = node
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

// AnnotatedString returns a String containing the meta data of the nodes
// for debugging purpose.
func (s *RGATreeSplit) AnnotatedString() string {
	var result []string

	node := s.initialHead
	for node != nil {
		if node.id.offset > 0 && node.insPrev == nil {
			log.Logger.Warn("insPrev should be presence")
		}

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

// cleanupRemovedNodes cleans up nodes that have been removed.
// The cleaned nodes are subject to garbage collector collection.
func (s *RGATreeSplit) cleanupRemovedNodes(ticket *time.Ticket) int {
	count := 0
	for _, node := range s.removedNodeMap {
		if node.removedAt != nil && ticket.Compare(node.removedAt) >= 0 {
			s.treeByIndex.Delete(node.indexNode)
			s.purge(node)
			s.treeByID.Remove(node.id)
			delete(s.removedNodeMap, removedNodeMapKey(node.createdAt().Key()))
			count++
		}
	}

	return count
}

// purge removes the node passed as a parameter from RGATreeSplit.
func (s *RGATreeSplit) purge(node *RGATreeSplitNode) {
	if node.prev != nil {
		node.prev.next = node.next
		if node.next != nil {
			node.next.prev = node.prev
		}
	} else {
		s.initialHead, node.next.prev = node.next, nil
	}
	node.next, node.prev = nil, nil

	if node.insPrev != nil {
		node.insPrev.insNext, node.insPrev = nil, nil
	}
	if node.insNext != nil {
		node.insNext.insPrev, node.insNext = nil, nil
	}
}
