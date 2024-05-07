package crdt

import "github.com/yorkie-team/yorkie/pkg/document/time"

// GCNode represents a common node with GC.
type GCNode interface {
	// GetID returns the IDString of this node.
	GetID() string

	// GetRemovedAt returns the removal time of this node.
	GetRemovedAt() *time.Ticket

	// Purge physically purges the given child of this node.
	Purge(node GCNode, ticket *time.Ticket) (int, error)
}

// GCTreeNode is a node type for generalized garbage collection.
type GCTreeNode struct {
	ID        string
	value     GCNode
	par       *GCTreeNode
	children  []*GCTreeNode
	removedAt *time.Ticket
}

// NewGCTreeNode creates a new instance of GCTreeNode.
func NewGCTreeNode(value GCNode, IDString ...string) *GCTreeNode {
	ret := &GCTreeNode{
		ID:        value.GetID(),
		value:     value,
		par:       nil,
		children:  make([]*GCTreeNode, 0),
		removedAt: value.GetRemovedAt(),
	}
	//gcTreeNode, ok := value.(*GCTreeNode)
	//if ok {
	//	ret.removedAt = gcTreeNode.GetRemovedAt()
	//}

	if len(IDString) > 0 {
		ret.ID = IDString[0]
	}

	return ret
}

// GetID returns the ID of this node.
func (n *GCTreeNode) GetID() string {
	return n.ID
}

// GetRemovedAt returns the removal time of this node.
func (n *GCTreeNode) GetRemovedAt() *time.Ticket {
	return n.value.GetRemovedAt()
}

// AddChild appends the given node to the end of the children.
func (n *GCTreeNode) AddChild(child *GCTreeNode) {
	child.par = n
	n.children = append(n.children, child)
}

// Purge physically purges the given child of this node.
func (n *GCTreeNode) Purge(node GCNode, executedAt *time.Ticket) (int, error) {
	return n.value.Purge(node, executedAt)
}
