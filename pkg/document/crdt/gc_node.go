package crdt

import "github.com/yorkie-team/yorkie/pkg/document/time"

type GCNode interface {
	// GetID returns id.
	GetID() string
	GetRemovedAt() *time.Ticket
	Purge(ticket *time.Ticket) (int, error)
}

type IterableNode struct {
	GCNode
	value     interface{}
	par       *IterableNode
	children  []*IterableNode
	removedAt *time.Ticket
}

func NewIterableNode(value interface{}) *IterableNode {
	ret := &IterableNode{
		value:    value,
		par:      nil,
		children: make([]*IterableNode, 0),
	}
	iterableNode, ok := value.(*IterableNode)
	if ok {
		ret.removedAt = iterableNode.GetRemovedAt()
	}

	return ret
}

func (n *IterableNode) GetID() string {
	if gcNode, ok := n.value.(GCNode); ok {
		return gcNode.GetID()
	}
	return ""
}

func (n *IterableNode) GetRemovedAt() *time.Ticket {
	if gcNode, ok := n.value.(GCNode); ok {
		return gcNode.GetRemovedAt()
	}
	return nil
}

func (n *IterableNode) AddChild(child interface{}) {
	childNode, ok := child.(*IterableNode)
	if ok {
		childNode.par = n
		n.children = append(n.children, childNode)
	}
}

func (n *IterableNode) Purge(ticket *time.Ticket) (int, error) {
	if gcNode, ok := n.value.(GCNode); ok {
		return gcNode.Purge(ticket)
	}
	return 0, nil
}
