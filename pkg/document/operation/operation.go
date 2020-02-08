package operation

import (
	"github.com/yorkie-team/yorkie/pkg/document/json"
	"github.com/yorkie-team/yorkie/pkg/document/time"
)

type Operation interface {
	Execute(root *json.Root) error
	ExecutedAt() *time.Ticket
	SetActor(id *time.ActorID)
	ParentCreatedAt() *time.Ticket
}
