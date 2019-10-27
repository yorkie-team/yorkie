package change

import (
	"github.com/hackerwins/rottie/pkg/document/json"
	"github.com/hackerwins/rottie/pkg/document/operation"
)

type Change struct {
	id         *ID
	message    string
	operations []operation.Operation
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
