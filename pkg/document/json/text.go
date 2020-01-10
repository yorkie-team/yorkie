package json

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/hackerwins/yorkie/pkg/document/time"
	"github.com/hackerwins/yorkie/pkg/llrb"
	"github.com/hackerwins/yorkie/pkg/log"
	"github.com/hackerwins/yorkie/pkg/splay"
)

var (
	initialTextNodeID = NewTextNodeID(time.InitialTicket, 0)
)

type TextNodeID struct {
	createdAt *time.Ticket
	offset    int
}

func NewTextNodeID(createdAt *time.Ticket, offset int) *TextNodeID {
	return &TextNodeID{
		createdAt: createdAt,
		offset:    offset,
	}
}

func (t *TextNodeID) Compare(other llrb.Key) int {
	o := other.(*TextNodeID)
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

func (t *TextNodeID) Equal(other *TextNodeID) bool {
	return t.Compare(other) == 0
}

func (t *TextNodeID) CreatedAt() *time.Ticket {
	return t.createdAt
}

func (t *TextNodeID) Offset() int {
	return t.offset
}

func (t *TextNodeID) split(offset int) *TextNodeID {
	return NewTextNodeID(t.createdAt, t.offset+offset)
}

// AnnotatedString returns a string containing the meta data of the node id
// for debugging purpose.
func (t *TextNodeID) AnnotatedString() string {
	return fmt.Sprintf("%s:%d", t.createdAt.AnnotatedString(), t.offset)
}

func (t *TextNodeID) hasSameCreatedAt(id *TextNodeID) bool {
	return t.createdAt.Compare(id.createdAt) == 0
}

type TextNodePos struct {
	id             *TextNodeID
	relativeOffset int
}

func NewTextNodePos(id *TextNodeID, offset int) *TextNodePos {
	return &TextNodePos{id, offset}
}

func (pos *TextNodePos) getAbsoluteID() *TextNodeID {
	return NewTextNodeID(pos.id.createdAt, pos.id.offset+pos.relativeOffset)
}

// AnnotatedString returns a string containing the meta data of the position
// for debugging purpose.
func (pos *TextNodePos) AnnotatedString() string {
	return fmt.Sprintf("%s:%d", pos.id.AnnotatedString(), pos.relativeOffset)
}

func (pos *TextNodePos) ID() *TextNodeID {
	return pos.id
}

func (pos *TextNodePos) RelativeOffset() int {
	return pos.relativeOffset
}

type TextNode struct {
	id        *TextNodeID
	indexNode *splay.Node
	value     string
	deletedAt *time.Ticket

	prev    *TextNode
	next    *TextNode
	insPrev *TextNode
	insNext *TextNode
}

func newTextNode(id *TextNodeID, value string) *TextNode {
	node := &TextNode{
		id:    id,
		value: value,
	}
	node.indexNode = splay.NewNode(node)

	return node
}

func (t *TextNode) ID() *TextNodeID {
	return t.id
}

func (t *TextNode) InsPrevID() *TextNodeID {
	if t.insPrev == nil {
		return nil
	}

	return t.insPrev.id
}

func (t *TextNode) contentLen() int {
	return utf8.RuneCountInString(t.value)
}

func (t *TextNode) Len() int {
	if t.deletedAt != nil {
		return 0
	}
	return t.contentLen()
}

func (t *TextNode) String() string {
	return t.value
}

// DeepCopy returns a new instance of this TextNode without structural info.
func (t *TextNode) DeepCopy() *TextNode {
	node := &TextNode{
		id:        t.id,
		value:     t.value,
		deletedAt: t.deletedAt,
	}
	node.indexNode = splay.NewNode(node)

	return node
}

func (t *TextNode) SetInsPrev(node *TextNode) {
	t.insPrev = node
	node.insNext = t
}

func (t *TextNode) setPrev(node *TextNode) {
	t.prev = node
	node.next = t
}

func (t *TextNode) split(offset int) *TextNode {
	return newTextNode(
		t.id.split(offset),
		t.splitContent(offset),
	)
}

func (t *TextNode) splitContent(offset int) string {
	value := t.value
	r := []rune(value)
	t.value = string(r[0:offset])
	return string(r[offset:])
}

func (t *TextNode) createdAt() *time.Ticket {
	return t.id.createdAt
}

// annotatedString returns a string containing the meta data of the node
// for debugging purpose.
func (t *TextNode) annotatedString() string {
	return fmt.Sprintf("%s %s", t.id.AnnotatedString(), t.value)
}

func (t *TextNode) delete(editedAt *time.Ticket, latestCreatedAt *time.Ticket) bool {
	if !t.createdAt().After(latestCreatedAt) &&
		(t.deletedAt == nil || editedAt.After(t.deletedAt)) {
		t.deletedAt = editedAt
		return true
	}
	return false
}

type RGATreeSplit struct {
	initialHead *TextNode
	treeByIndex *splay.Tree
	treeByID    *llrb.Tree
}

func NewRGATreeSplit() *RGATreeSplit {
	initialHead := newTextNode(initialTextNodeID, "")
	treeByIndex := splay.NewTree()
	treeByID := llrb.NewTree()

	treeByIndex.Insert(initialHead.indexNode)
	treeByID.Put(initialHead.ID(), initialHead)

	return &RGATreeSplit{
		initialHead: initialHead,
		treeByIndex: treeByIndex,
		treeByID:    treeByID,
	}
}

func (s *RGATreeSplit) createRange(from, to int) (*TextNodePos, *TextNodePos) {
	fromPos := s.findTextNodePos(from)
	if from == to {
		return fromPos, fromPos
	}

	return fromPos, s.findTextNodePos(to)
}

func (s *RGATreeSplit) findTextNodePos(index int) *TextNodePos {
	splayNode, offset := s.treeByIndex.Find(index)
	textNode := splayNode.Value().(*TextNode)
	return &TextNodePos{
		id:             textNode.ID(),
		relativeOffset: offset,
	}
}

func (s *RGATreeSplit) findTextNodeWithSplit(
	pos *TextNodePos,
	editedAt *time.Ticket,
) (*TextNode, *TextNode) {
	absoluteID := pos.getAbsoluteID()
	node := s.findFloorTextNodePreferToLeft(absoluteID)

	relativeOffset := absoluteID.offset - node.id.offset

	s.splitTextNode(node, relativeOffset)

	for node.next != nil && node.next.createdAt().After(editedAt) {
		node = node.next
	}

	return node, node.next
}

func (s *RGATreeSplit) findFloorTextNodePreferToLeft(id *TextNodeID) *TextNode {
	node := s.findFloorTextNode(id)
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

func (s *RGATreeSplit) splitTextNode(node *TextNode, offset int) *TextNode {
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

func (s *RGATreeSplit) InsertAfter(prev *TextNode, node *TextNode) *TextNode {
	next := prev.next
	node.setPrev(prev)
	if next != nil {
		next.setPrev(node)
	}

	s.treeByID.Put(node.id, node)
	s.treeByIndex.InsertAfter(prev.indexNode, node.indexNode)

	return node
}

func (s *RGATreeSplit) InitialHead() *TextNode {
	return s.initialHead
}

func (s *RGATreeSplit) FindTextNode(id *TextNodeID) *TextNode {
	if id == nil {
		return nil
	}

	return s.findFloorTextNode(id)
}

func (s *RGATreeSplit) findFloorTextNode(id *TextNodeID) *TextNode {
	key, value := s.treeByID.Floor(id)
	if key == nil {
		return nil
	}

	foundID := key.(*TextNodeID)
	foundValue := value.(*TextNode)

	if !foundID.Equal(id) && !foundID.hasSameCreatedAt(id) {
		return nil
	}

	return foundValue
}

func (s *RGATreeSplit) edit(
	from *TextNodePos,
	to *TextNodePos,
	latestCreatedAtMapByActor map[string]*time.Ticket,
	content string,
	editedAt *time.Ticket,
) (*TextNodePos, map[string]*time.Ticket) {
	// 01. split nodes with from and to
	fromLeft, fromRight := s.findTextNodeWithSplit(from, editedAt)
	toLeft, toRight := s.findTextNodeWithSplit(to, editedAt)

	// 02. delete between from and to
	nodesToDelete := s.findBetween(fromRight, toRight)
	latestCreatedAtMap := s.deleteNodes(nodesToDelete, latestCreatedAtMapByActor, editedAt)

	var caretID *TextNodeID
	if toRight == nil {
		caretID = toLeft.id
	} else {
		caretID = toRight.id
	}
	caretPos := NewTextNodePos(caretID, 0)

	// 03. insert a new node
	if content != "" {
		inserted := s.InsertAfter(fromLeft, newTextNode(NewTextNodeID(editedAt, 0), content))
		caretPos = NewTextNodePos(inserted.id, inserted.contentLen())
	}

	return caretPos, latestCreatedAtMap
}

func (s *RGATreeSplit) findBetween(from *TextNode, to *TextNode) []*TextNode {
	current := from
	var nodes []*TextNode
	for current != nil && current != to {
		nodes = append(nodes, current)
		current = current.next
	}
	return nodes
}

func (s *RGATreeSplit) deleteNodes(
	candidates []*TextNode,
	latestCreatedAtMapByActor map[string]*time.Ticket,
	editedAt *time.Ticket,
) map[string]*time.Ticket {
	createdAtMapByActor := make(map[string]*time.Ticket)

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

		if node.delete(editedAt, latestCreatedAt) {
			s.treeByIndex.Splay(node.indexNode)

			latestCreatedAt := createdAtMapByActor[actorIDHex]
			createdAt := node.id.createdAt
			if latestCreatedAt == nil || createdAt.After(latestCreatedAt) {
				createdAtMapByActor[actorIDHex] = createdAt
			}
		}
	}

	return createdAtMapByActor
}

func (s *RGATreeSplit) marshal() string {
	var values []string

	node := s.initialHead.next
	for node != nil {
		if node.deletedAt == nil {
			values = append(values, node.value)
		}
		node = node.next
	}

	return strings.Join(values, "")
}

func (s *RGATreeSplit) textNodes() []*TextNode {
	var nodes []*TextNode

	node := s.initialHead.next
	for node != nil {
		nodes = append(nodes, node)
		node = node.next
	}

	return nodes
}

// AnnotatedString returns a string containing the meta data of the nodes
// for debugging purpose.
func (s *RGATreeSplit) AnnotatedString() string {
	var result []string

	node := s.initialHead
	for node != nil {
		if node.id.offset > 0 && node.insPrev == nil {
			log.Logger.Warn("insPrev should be presence")
		}

		if node.deletedAt != nil {
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

type Selection struct {
	from      *TextNodePos
	to        *TextNodePos
	updatedAt *time.Ticket
}

func newSelection(from, to *TextNodePos, updatedAt *time.Ticket) *Selection {
	return &Selection{
		from,
		to,
		updatedAt,
	}
}

// Text is an extended data type for the contents of a text editor.
type Text struct {
	rgaTreeSplit *RGATreeSplit
	selectionMap map[string]*Selection
	createdAt    *time.Ticket
}

// NewText creates a new instance of Text.
func NewText(elements *RGATreeSplit, createdAt *time.Ticket) *Text {
	return &Text{
		rgaTreeSplit: elements,
		selectionMap: make(map[string]*Selection),
		createdAt:    createdAt,
	}
}

func (t *Text) Marshal() string {
	return fmt.Sprintf("\"%s\"", t.rgaTreeSplit.marshal())
}

func (t *Text) Deepcopy() Element {
	rgaTreeSplit := NewRGATreeSplit()

	current := rgaTreeSplit.InitialHead()
	for _, textNode := range t.TextNodes() {
		current = rgaTreeSplit.InsertAfter(current, textNode.DeepCopy())
		insPrevID := textNode.InsPrevID()
		if insPrevID != nil {
			insPrevNode := rgaTreeSplit.FindTextNode(insPrevID)
			if insPrevNode == nil {
				log.Logger.Warn("insPrevNode should be presence")
			}
			current.SetInsPrev(insPrevNode)
		}
	}

	return NewText(rgaTreeSplit, t.createdAt)
}

// CreatedAt returns the creation time of this Text.
func (t *Text) CreatedAt() *time.Ticket {
	return t.createdAt
}

// CreateRange returns pair of TextNodePos of the given integer offsets.
func (t *Text) CreateRange(from, to int) (*TextNodePos, *TextNodePos) {
	return t.rgaTreeSplit.createRange(from, to)
}

func (t *Text) Edit(
	from,
	to *TextNodePos,
	latestCreatedAtMapByActor map[string]*time.Ticket,
	content string,
	editedAt *time.Ticket,
) (*TextNodePos, map[string]*time.Ticket) {
	cursorPos, latestCreatedAtMapByActor := t.rgaTreeSplit.edit(
		from,
		to,
		latestCreatedAtMapByActor,
		content,
		editedAt,
	)
	log.Logger.Debugf(
		"EDIT: '%s' edits %s",
		editedAt.ActorID().String(),
		t.rgaTreeSplit.AnnotatedString(),
	)
	return cursorPos, latestCreatedAtMapByActor
}

func (t *Text) Select(
	from *TextNodePos,
	to *TextNodePos,
	updatedAt *time.Ticket,
) {
	if _, ok := t.selectionMap[updatedAt.ActorIDHex()]; !ok {
		t.selectionMap[updatedAt.ActorIDHex()] = newSelection(from, to, updatedAt)
		return
	}

	prevSelection := t.selectionMap[updatedAt.ActorIDHex()]
	if updatedAt.After(prevSelection.updatedAt) {
		log.Logger.Debugf(
			"SELT: '%s' selects %s",
			updatedAt.ActorID().String(),
			t.rgaTreeSplit.AnnotatedString(),
		)

		t.selectionMap[updatedAt.ActorIDHex()] = newSelection(from, to, updatedAt)
	}
}

func (t *Text) TextNodes() []*TextNode {
	return t.rgaTreeSplit.textNodes()
}

// AnnotatedString returns a string containing the meta data of the text
// for debugging purpose.
func (t *Text) AnnotatedString() string {
	return t.rgaTreeSplit.AnnotatedString()
}
