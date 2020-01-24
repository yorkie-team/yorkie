package change

import (
	"github.com/hackerwins/yorkie/pkg/document/json"
	"github.com/hackerwins/yorkie/pkg/document/operation"
	"github.com/hackerwins/yorkie/pkg/document/time"
)

// Context is used to record the context of modification when editing a document.
// Each time we add an operation, a new time ticket is issued.
// Finally returns a Change after the modification has been completed.
type Context struct {
	id         *ID
	message    string
	operations []operation.Operation
	delimiter  uint32
	root       *json.Root
}

// NewContext creates a new instance of Context.
func NewContext(id *ID, message string, root *json.Root) *Context {
	return &Context{
		id:      id,
		message: message,
		root: root,
	}
}

// ID returns ID.
func (c *Context) ID() *ID {
	return c.id
}

// ToChange creates a new change of this context.
func (c *Context) ToChange() *Change {
	return New(c.id, c.message, c.operations)
}

// HasOperations returns whether this change has operations or not.
func (c *Context) HasOperations() bool {
	return len(c.operations) > 0
}

// IssueTimeTicket creates a time ticket to be used to create a new operation.
func (c *Context) IssueTimeTicket() *time.Ticket {
	c.delimiter++
	return c.id.NewTimeTicket(c.delimiter)
}

// Push pushes an new operation into context queue.
func (c *Context) Push(op operation.Operation) {
	c.operations = append(c.operations, op)
}

func (c *Context) RegisterElement(elem json.Element) {
	c.root.RegisterElement(elem)
}
