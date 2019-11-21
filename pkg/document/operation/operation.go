package operation

import (
	"github.com/hackerwins/yorkie/pkg/document/json"
	"github.com/hackerwins/yorkie/pkg/document/time"
)

type Operation interface {
	Execute(root *json.Root) error
	ExecutedAt() *time.Ticket
	SetActor(id *time.ActorID)
	ParentCreatedAt() *time.Ticket
}
