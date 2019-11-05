package change

import (
	"github.com/hackerwins/rottie/pkg/document/json"
	"github.com/hackerwins/rottie/pkg/document/operation"
	"github.com/hackerwins/rottie/pkg/document/time"
)

type Change struct {
	id         *ID
	message    string
	operations []operation.Operation
	serverSeq  *uint64
}

func New(id *ID, message string, operations []operation.Operation) *Change {
	return &Change{
		id:         id,
		message:    message,
		operations: operations,
	}
}

func (c *Change) Execute(root *json.Root) error {
	for _, op := range c.operations {
		if err := op.Execute(root); err != nil {
			return err
		}
	}
	return nil
}

func (c *Change) ID() *ID {
	return c.id
}

func (c *Change) Message() string {
	return c.message
}

func (c *Change) Operations() []operation.Operation {
	return c.operations
}

func (c *Change) SetServerSeq(serverSeq uint64) {
	c.serverSeq = &serverSeq
}

func (c *Change) ServerSeq() uint64 {
	return *c.serverSeq
}

func (c *Change) ClientSeq() uint32 {
	return c.id.ClientSeq()
}

func (c *Change) SetActor(actor *time.ActorID) {
	c.id = c.id.SetActor(actor)
	for _, op := range c.operations {
		op.SetActor(actor)
	}
}
