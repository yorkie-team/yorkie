package operation

import (
	"fmt"

	"github.com/hackerwins/yorkie/pkg/document/json"
	"github.com/hackerwins/yorkie/pkg/document/json/datatype"
	"github.com/hackerwins/yorkie/pkg/document/time"
	"github.com/hackerwins/yorkie/pkg/log"
)

type Set struct {
	parentCreatedAt *time.Ticket
	executedAt      *time.Ticket
	key             string
	value           datatype.Element
}

func NewSet(
	key string,
	value datatype.Element,
	parentCreatedAt *time.Ticket,
	executedAt *time.Ticket,
) *Set {
	return &Set{
		key:             key,
		value:           value,
		parentCreatedAt: parentCreatedAt,
		executedAt:      executedAt,
	}
}

func (o *Set) Execute(root *json.Root) error {
	parent := root.FindByCreatedAt(o.parentCreatedAt)

	obj, ok := parent.(*json.Object)
	if !ok {
		err := fmt.Errorf("fail to execute, only Object can execute Set")
		log.Logger.Error(err)
		return err
	}

	obj.Set(o.key, o.value)
	root.RegisterElement(o.value)
	return nil
}

func (o *Set) ParentCreatedAt() *time.Ticket {
	return o.parentCreatedAt
}

func (o *Set) ExecutedAt() *time.Ticket {
	return o.executedAt
}

func (o *Set) SetActor(actorID *time.ActorID) {
	o.executedAt = o.executedAt.SetActorID(actorID)
}

func (o *Set) Key() string {
	return o.key
}

func (o *Set) Value() datatype.Element {
	return o.value
}
