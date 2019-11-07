package datatype

import (
	"strings"

	"github.com/hackerwins/yorkie/pkg/document/time"
)

type Node struct {
	element Element
	prev    *Node
	next    *Node
}

func newNode(element Element) *Node {
	return &Node{
		element: element,
	}
}

func newNodeAfter(prev *Node, element Element) *Node {
	newNode := newNode(element)
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
	nodeTableByCreatedAt map[string]*Node
	first                *Node
	last                 *Node
	size                 int
}

func NewRGA() *RGA {
	nodeTableByCreatedAt := make(map[string]*Node)
	dummyHead := newNode(NewPrimitive("", time.InitialTicket))
	nodeTableByCreatedAt[dummyHead.element.CreatedAt().Key()] = dummyHead

	return &RGA{
		nodeTableByCreatedAt: nodeTableByCreatedAt,
		first:                dummyHead,
		last:                 dummyHead,
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

		sb.WriteString(current.element.Marshal())
		if a.size-1 != idx {
			sb.WriteString(",")
		}

		current = current.next
		idx += 1
	}

	sb.WriteString("]")

	return sb.String()
}

func (a *RGA) Add(e Element) {
	a.insertAfterInternal(a.last, e)
}

func (a *RGA) Elements() []Element {
	var elements []Element
	current := a.first.next
	for {
		if current == nil {
			break
		}

		elements = append(elements, current.element)
		current = current.next
	}

	return elements
}

func (a *RGA) LastCreatedAt() *time.Ticket {
	return a.last.element.CreatedAt()
}

func (a *RGA) InsertAfter(prevCreatedAt *time.Ticket, element Element) {
	prevNode := a.findByCreatedAt(prevCreatedAt, element.CreatedAt())
	a.insertAfterInternal(prevNode, element)
}

func (a *RGA) findByCreatedAt(prevCreatedAt *time.Ticket, createdAt *time.Ticket) *Node {
	node := a.nodeTableByCreatedAt[prevCreatedAt.Key()]
	for node.next != nil && createdAt.CompareTo(node.next.element.CreatedAt()) < 0 {
		node = node.next
	}

	return node
}

func (a *RGA) insertAfterInternal(prev *Node, element Element) {
	newNode := newNodeAfter(prev, element)
	if prev == a.last {
		a.last = newNode
	}

	a.size += 1
	a.nodeTableByCreatedAt[element.CreatedAt().Key()] = newNode
}

