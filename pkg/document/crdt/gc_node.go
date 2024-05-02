package crdt

import "github.com/yorkie-team/yorkie/pkg/document/time"

// GCNode represents a common node with GC.
type GCNode interface {
	// GetID returns the IDString of this node.
	GetID() string
	// GetRemovedAt returns the removal time of this node.
	GetRemovedAt() *time.Ticket
	// Purge physically purges children of given node.
	Purge(ticket *time.Ticket) (int, error)
}

// IterableNode is a node type for generalized garbage collection.
type IterableNode struct {
	value     GCNode
	par       *IterableNode
	children  []*IterableNode
	removedAt *time.Ticket
}

// NewIterableNode creates a new instance of IterableNode.
func NewIterableNode(value GCNode) *IterableNode {
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
	return n.value.GetID()
}

// GetRemovedAt returns the removal time of this IterableNode.
func (n *IterableNode) GetRemovedAt() *time.Ticket {
	return n.value.GetRemovedAt()
}

// AddChild appends the given node to the end of the children.
func (n *IterableNode) AddChild(child *IterableNode) {
	child.par = n
	n.children = append(n.children, child)
}

// Purge physically purges IterableNode that have been removed.
func (n *IterableNode) Purge(ticket *time.Ticket) (int, error) {
	return n.value.Purge(ticket)
}
