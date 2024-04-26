package crdt

import "github.com/yorkie-team/yorkie/pkg/document/time"

// GCNode represents ..writing comment is one of the hardest work for me, so I left temporarily left as TODO.
type GCNode interface {
	// GetID returns id.
	GetID() string
	GetRemovedAt() *time.Ticket
	Purge(ticket *time.Ticket) (int, error)
}

// IterableNode represents ..writing comment is one of the hardest work for me, so I left temporarily left as TODO.
type IterableNode struct {
	GCNode
	value     interface{}
	par       *IterableNode
	children  []*IterableNode
	removedAt *time.Ticket
}

// NewIterableNode represents ..writing comment is one of the hardest work for me, so I left temporarily left as TODO.
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

// GetID represents ..writing comment is one of the hardest work for me, so I left temporarily left as TODO.
func (n *IterableNode) GetID() string {
	if gcNode, ok := n.value.(GCNode); ok {
		return gcNode.GetID()
	}
	return ""
}

// GetRemovedAt represents ..writing comment is one of the hardest work for me, so I left temporarily left as TODO.
func (n *IterableNode) GetRemovedAt() *time.Ticket {
	if gcNode, ok := n.value.(GCNode); ok {
		return gcNode.GetRemovedAt()
	}
	return nil
}

// AddChild represents ..writing comment is one of the hardest work for me, so I left temporarily left as TODO.
func (n *IterableNode) AddChild(child interface{}) {
	childNode, ok := child.(*IterableNode)
	if ok {
		childNode.par = n
		n.children = append(n.children, childNode)
	}
}

// Purge represents ..writing comment is one of the hardest work for me, so I left temporarily left as TODO.
func (n *IterableNode) Purge(ticket *time.Ticket) (int, error) {
	if gcNode, ok := n.value.(GCNode); ok {
		return gcNode.Purge(ticket)
	}
	return 0, nil
}
