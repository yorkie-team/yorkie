package operation

import (
	"fmt"

	"github.com/hackerwins/yorkie/pkg/document/json"
	"github.com/hackerwins/yorkie/pkg/document/time"
	"github.com/hackerwins/yorkie/pkg/log"
)

type Edit struct {
	parentCreatedAt           *time.Ticket
	from                      *json.TextNodePos
	to                        *json.TextNodePos
	latestCreatedAtMapByActor map[string]*time.Ticket
	content                   string
	executedAt                *time.Ticket
}

func NewEdit(
	parentCreatedAt *time.Ticket,
	from *json.TextNodePos,
	to *json.TextNodePos,
	latestCreatedAtMapByActor map[string]*time.Ticket,
	content string,
	executedAt *time.Ticket,
) *Edit {
	return &Edit{
		parentCreatedAt:           parentCreatedAt,
		from:                      from,
		to:                        to,
		latestCreatedAtMapByActor: latestCreatedAtMapByActor,
		content:                   content,
		executedAt:                executedAt,
	}
}

func (e *Edit) Execute(root *json.Root) error {
	parent := root.FindByCreatedAt(e.parentCreatedAt)
	obj, ok := parent.(*json.Text)
	if !ok {
		err := fmt.Errorf("fail to execute, only Text can execute Edit")
		log.Logger.Error(err)
		return err
	}

	obj.Edit(e.from, e.to, e.latestCreatedAtMapByActor, e.content, e.executedAt)
	return nil
}

func (e *Edit) From() *json.TextNodePos {
	return e.from
}

func (e *Edit) To() *json.TextNodePos {
	return e.to
}

func (e *Edit) ExecutedAt() *time.Ticket {
	return e.executedAt
}

func (e *Edit) SetActor(actorID *time.ActorID) {
	e.executedAt = e.executedAt.SetActorID(actorID)
}
func (e *Edit) ParentCreatedAt() *time.Ticket {
	return e.parentCreatedAt
}

func (e *Edit) Content() string {
	return e.content
}

func (e *Edit) CreatedAtMapByActor() map[string]*time.Ticket {
	return e.latestCreatedAtMapByActor
}
