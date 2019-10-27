package operation

import (
	"fmt"

	"github.com/hackerwins/rottie/pkg/document/json/datatype"

	"github.com/hackerwins/rottie/pkg/document/json"
	"github.com/hackerwins/rottie/pkg/document/time"
	"github.com/hackerwins/rottie/pkg/log"
)

type Add struct {
	value           datatype.Element
	parentCreatedAt *time.Ticket
	prevCreatedAt   *time.Ticket
	executedAt      *time.Ticket
}

func NewAdd(
	value datatype.Element,
	parentCreatedAt *time.Ticket,
	prevCreatedAt *time.Ticket,
	executedAt *time.Ticket,
) *Add {
	return &Add{
		value:           value,
		parentCreatedAt: parentCreatedAt,
		prevCreatedAt:   prevCreatedAt,
		executedAt:      executedAt,
	}
}

func (o *Add) Execute(root *json.Root) error {
	parent := root.FindByCreatedAt(o.parentCreatedAt)

	obj, ok := parent.(*json.Array)
	if !ok {
		err := fmt.Errorf("fail to execute, only Array can execute Set")
		log.Logger.Error(err)
		return err
	}

	obj.Add(o.value)
	root.RegisterElement(o.value)
	return nil
}

func (o *Add) ExecutedAt() *time.Ticket {
	return o.executedAt
}
