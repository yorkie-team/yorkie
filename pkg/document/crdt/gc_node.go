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
