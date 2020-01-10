package operation

import (
	"fmt"

	"github.com/hackerwins/yorkie/pkg/document/json"
	"github.com/hackerwins/yorkie/pkg/document/time"
	"github.com/hackerwins/yorkie/pkg/log"
)

type Select struct {
	parentCreatedAt           *time.Ticket
	from                      *json.TextNodePos
	to                        *json.TextNodePos
	executedAt                *time.Ticket
}

func NewSelect(
	parentCreatedAt *time.Ticket,
	from *json.TextNodePos,
	to *json.TextNodePos,
	executedAt *time.Ticket,
) *Select {
	return &Select{
		parentCreatedAt:           parentCreatedAt,
		from:                      from,
		to:                        to,
		executedAt:                executedAt,
	}
}

func (s *Select) Execute(root *json.Root) error {
	parent := root.FindByCreatedAt(s.parentCreatedAt)
	obj, ok := parent.(*json.Text)
	if !ok {
		err := fmt.Errorf("fail to execute, only Text can execute Edit")
		log.Logger.Error(err)
		return err
	}

	obj.Select(s.from, s.to, s.executedAt)
	return nil
}

func (s *Select) From() *json.TextNodePos {
	return s.from
}

func (s *Select) To() *json.TextNodePos {
	return s.to
}

func (s *Select) ExecutedAt() *time.Ticket {
	return s.executedAt
}

func (s *Select) SetActor(actorID *time.ActorID) {
	s.executedAt = s.executedAt.SetActorID(actorID)
}
func (s *Select) ParentCreatedAt() *time.Ticket {
	return s.parentCreatedAt
}
