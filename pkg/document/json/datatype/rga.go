package datatype

import (
	"strings"

	"github.com/hackerwins/yorkie/pkg/log"

	"github.com/hackerwins/yorkie/pkg/document/time"
)

type RGANode struct {
	prev      *RGANode
	next      *RGANode
	value     Element
	isRemoved bool
}

func newRGANode(elem Element) *RGANode {
	return &RGANode{
		prev:      nil,
		next:      nil,
		value:     elem,
		isRemoved: false,
	}
}

func newNodeAfter(prev *RGANode, element Element) *RGANode {
	newNode := newRGANode(element)
	prevNext := prev.next

	prev.next = newNode
	newNode.prev = prev
	newNode.next = prevNext
	if prevNext != nil {
		prevNext.prev = newNode
	}

	return prev.next
}

// RGA is replicated growable array.
type RGA struct {
	nodeMapByCreatedAt map[string]*RGANode
	first              *RGANode
	last               *RGANode
	size               int
}

func NewRGA() *RGA {
	nodeMapByCreatedAt := make(map[string]*RGANode)
	dummyHead := newRGANode(NewPrimitive("", time.InitialTicket))
	nodeMapByCreatedAt[dummyHead.value.CreatedAt().Key()] = dummyHead

	return &RGA{
		nodeMapByCreatedAt: nodeMapByCreatedAt,
		first:              dummyHead,
		last:               dummyHead,
		size:               0,
	}
}

func (a *RGA) Marshal() string {
	sb := strings.Builder{}
	sb.WriteString("[")

	idx := 0
	current := a.first.next
	for {
		if current == nil {
			break
		}

		if !current.isRemoved {
			sb.WriteString(current.value.Marshal())
			if a.size-1 != idx {
				sb.WriteString(",")
			}
			idx++
		}

		current = current.next
	}

	sb.WriteString("]")

	return sb.String()
}

func (a *RGA) Add(e Element) {
	a.insertAfter(a.last, e)
}

func (a *RGA) Elements() []Element {
	var elements []Element
	current := a.first.next
	for {
		if current == nil {
			break
		}

		if !current.isRemoved {
			elements = append(elements, current.value)
		}

		current = current.next
	}

	return elements
}

func (a *RGA) LastCreatedAt() *time.Ticket {
	return a.last.value.CreatedAt()
}

func (a *RGA) InsertAfter(prevCreatedAt *time.Ticket, element Element) {
	prevNode := a.findByCreatedAt(prevCreatedAt, element.CreatedAt())
	a.insertAfter(prevNode, element)
}

func (a *RGA) Get(idx int) Element {
	// TODO introduce LLRBTree for improving upstream performance
	elements := a.Elements()
	if len(elements) <= idx {
		return nil
	}

	return elements[idx]
}

func (a *RGA) RemoveByCreatedAt(createdAt *time.Ticket) Element {
	if node, ok := a.nodeMapByCreatedAt[createdAt.Key()]; ok {
		node.isRemoved = true
		a.size--
		return node.value
	}

	log.Logger.Warn("fail to find ", createdAt.Key())
	return nil
}

func (a *RGA) Len() int {
	return a.size
}

func (a *RGA) findByCreatedAt(prevCreatedAt *time.Ticket, createdAt *time.Ticket) *RGANode {
	node := a.nodeMapByCreatedAt[prevCreatedAt.Key()]
	for node.next != nil && createdAt.After(node.next.value.CreatedAt()) {
		node = node.next
	}

	return node
}

func (a *RGA) insertAfter(prev *RGANode, element Element) {
	newNode := newNodeAfter(prev, element)
	if prev == a.last {
		a.last = newNode
	}

	a.size++
	a.nodeMapByCreatedAt[element.CreatedAt().Key()] = newNode
}
