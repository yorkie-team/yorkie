package crdt

import "github.com/yorkie-team/yorkie/pkg/document/time"

// GCNode represents a common node with GC.
type GCNode interface {
	// GetID returns id.
	GetID() string
	GetRemovedAt() *time.Ticket
	Purge(ticket *time.Ticket) (int, error)
}

// IterableNode is a node type for generalized garbage collection.
type IterableNode struct {
	GCNode
	value     interface{}
	par       *IterableNode
	children  []*IterableNode
	removedAt *time.Ticket
}

// NewIterableNode creates a new instance of IterableNode.
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

// GetID returns the ID of this IterableNode.
func (n *IterableNode) GetID() string {
	if gcNode, ok := n.value.(GCNode); ok {
		return gcNode.GetID()
	}
	return ""
}

// GetRemovedAt returns the removal time of this IterableNode.
func (n *IterableNode) GetRemovedAt() *time.Ticket {
	if gcNode, ok := n.value.(GCNode); ok {
		return gcNode.GetRemovedAt()
	}
	return nil
}

// AddChild appends the given node to the end of the children.
func (n *IterableNode) AddChild(child interface{}) {
	childNode, ok := child.(*IterableNode)
	if ok {
		childNode.par = n
		n.children = append(n.children, childNode)
	}
}

// Purge physically purges IterableNode that have been removed.
func (n *IterableNode) Purge(ticket *time.Ticket) (int, error) {
	if gcNode, ok := n.value.(GCNode); ok {
		return gcNode.Purge(ticket)
	}
	return 0, nil
}
