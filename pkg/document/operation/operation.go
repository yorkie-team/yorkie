package operation

import (
	"github.com/hackerwins/rottie/pkg/document/json"
	"github.com/hackerwins/rottie/pkg/document/time"
)

type Operation interface {
	Execute(root *json.Root) error
	ExecutedAt() *time.Ticket
	SetActor(id *time.ActorID)
}
