package change

import (
	"github.com/hackerwins/yorkie/pkg/document/json"
	"github.com/hackerwins/yorkie/pkg/document/operation"
	"github.com/hackerwins/yorkie/pkg/document/time"
)

// Change represents a unit of modification in the document.
// And also Change is mutable struct.
type Change struct {
	id *ID
	// message is used to save a description of the change.
	message string
	// operations represent a series of user edits.
	operations []operation.Operation
	// serverSeq is optional and only present for changes stored on the server.
	serverSeq *uint64
}

// New creates a new instance of Change.
func New(id *ID, message string, operations []operation.Operation) *Change {
	return &Change{
		id:         id,
		message:    message,
		operations: operations,
	}
}

// Execute applies this change to the given JSON root.
func (c *Change) Execute(root *json.Root) error {
	for _, op := range c.operations {
		if err := op.Execute(root); err != nil {
			return err
		}
	}
	return nil
}

// ID returns the ID of this change.
func (c *Change) ID() *ID {
	return c.id
}

// Message returns the message of this change.
func (c *Change) Message() string {
	return c.message
}

// Operations returns the operations of this change.
func (c *Change) Operations() []operation.Operation {
	return c.operations
}

// SetServerSeq sets the given serverSeq.
func (c *Change) SetServerSeq(serverSeq uint64) {
	c.serverSeq = &serverSeq
}

// ServerSeq returns the serverSeq of this change.
func (c *Change) ServerSeq() uint64 {
	return *c.serverSeq
}

// ClientSeq returns the clientSeq of this change.
func (c *Change) ClientSeq() uint32 {
	return c.id.ClientSeq()
}

// SetActor sets the given actor.
func (c *Change) SetActor(actor *time.ActorID) {
	c.id = c.id.SetActor(actor)
	for _, op := range c.operations {
		op.SetActor(actor)
	}
}
