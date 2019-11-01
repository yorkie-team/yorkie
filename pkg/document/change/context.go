package change

import (
	"github.com/hackerwins/rottie/pkg/document/operation"
	"github.com/hackerwins/rottie/pkg/document/time"
)

type Context struct {
	id         *ID
	message    string
	operations []operation.Operation
	delimiter  uint32
}

func NewContext(id *ID, message string) *Context {
	return &Context{
		id:      id,
		message: message,
	}
}

func (c *Context) ID() *ID {
	return c.id
}

func (c *Context) ToChange() *Change {
	return New(c.id, c.message, c.operations)
}

func (c *Context) HasOperations() bool {
	return len(c.operations) > 0
}

func (c *Context) IssueTimeTicket() *time.Ticket {
	c.delimiter++
	return c.id.NewTimeTicket(c.delimiter)
}

func (c *Context) Push(op operation.Operation) {
	c.operations = append(c.operations, op)
}
