package crdt

import "github.com/yorkie-team/yorkie/pkg/document/time"

// GCNode represents a common node with GC.
type GCNode interface {
	// GetID returns the IDString of this node.
	GetID() string
	// GetRemovedAt returns the removal time of this node.
	GetRemovedAt() *time.Ticket
	// Purge physically purges children of given node.
	//Purge(ticket *time.Ticket) (int, error)
	Purge(node GCNode, ticket *time.Ticket) (int, error)
}

// IterableNode is a node type for generalized garbage collection.
type IterableNode struct {
	ID        string
	value     GCNode
	par       *IterableNode
	children  []*IterableNode
	removedAt *time.Ticket
}

// NewIterableNode creates a new instance of IterableNode.
func NewIterableNode(value GCNode, IDString ...string) *IterableNode {
	ret := &IterableNode{
		ID:       value.GetID(),
		value:    value,
		par:      nil,
		children: make([]*IterableNode, 0),
	}
	iterableNode, ok := value.(*IterableNode)
	if ok {
		ret.removedAt = iterableNode.GetRemovedAt()
	}
	if len(IDString) > 0 {
		ret.ID = IDString[0]
	}

	return ret
}

// GetID returns the ID of this node.
func (n *IterableNode) GetID() string {
	return n.ID
}

// GetRemovedAt returns the removal time of this node.
func (n *IterableNode) GetRemovedAt() *time.Ticket {
	return n.value.GetRemovedAt()
}

// AddChild appends the given node to the end of the children.
func (n *IterableNode) AddChild(child *IterableNode) {
	child.par = n
	n.children = append(n.children, child)
}

// Purge physically purges children of given node that have been removed.
func (n *IterableNode) Purge(node GCNode, executedAt *time.Ticket) (int, error) {
	return n.value.Purge(node, executedAt)
}
