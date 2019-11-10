package operation

import (
	"fmt"
	"github.com/hackerwins/yorkie/pkg/document/json"
	"github.com/hackerwins/yorkie/pkg/document/json/datatype"
	"github.com/hackerwins/yorkie/pkg/document/time"
	"github.com/hackerwins/yorkie/pkg/log"
)

type Remove struct {
	parentCreatedAt *time.Ticket
	createdAt       *time.Ticket
	executedAt      *time.Ticket
}

func NewRemove(
	parentCreatedAt *time.Ticket,
	createdAt *time.Ticket,
	executedAt *time.Ticket,
) *Remove {
	return &Remove{
		parentCreatedAt: parentCreatedAt,
		createdAt:       createdAt,
		executedAt:      executedAt,
	}
}

func (o *Remove) Execute(root *json.Root) error {
	parent := root.FindByCreatedAt(o.parentCreatedAt)

	var removed datatype.Element
	switch obj := parent.(type) {
	case *json.Object:
		removed = obj.Remove(o.createdAt)
	case *json.Array:
		removed = obj.Remove(o.createdAt)
	default:
		err := fmt.Errorf("fail to execute, only Object, Array can execute Remove")
		log.Logger.Error(err)
		return err
	}

	for _, descendant := range json.GetDescendants(removed) {
		root.DeregisterElement(descendant.CreatedAt())
	}
	return nil
}

func (o *Remove) ParentCreatedAt() *time.Ticket {
	return o.parentCreatedAt
}

func (o *Remove) ExecutedAt() *time.Ticket {
	return o.executedAt
}

func (o *Remove) SetActor(actorID *time.ActorID) {
	o.executedAt = o.executedAt.SetActorID(actorID)
}

func (o *Remove) CreatedAt() *time.Ticket {
	return o.createdAt
}
