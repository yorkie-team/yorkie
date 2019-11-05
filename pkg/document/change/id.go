package change

import (
	"github.com/hackerwins/rottie/pkg/document/time"
)

var (
	InitialID = NewID(0, 0, time.InitialActorID)
)

type ID struct {
	clientSeq uint32
	lamport   uint64
	actor     *time.ActorID
}

func NewID(
	clientSeq uint32,
	lamport uint64,
	actorID *time.ActorID,
) *ID {
	id := &ID{
		clientSeq: clientSeq,
		lamport:   lamport,
		actor:     actorID,
	}

	return id
}

func (id *ID) Next() *ID {
	return &ID{
		clientSeq: id.clientSeq + 1,
		lamport:   id.lamport + 1,
		actor:     id.actor,
	}
}

func (id *ID) NewTimeTicket(delimiter uint32) *time.Ticket {
	return time.NewTicket(
		id.lamport,
		delimiter,
		id.actor,
	)
}

func (id *ID) Sync(other *ID) *ID {
	if id.lamport < other.lamport {
		return NewID(id.clientSeq, other.lamport, id.actor)
	}

	return NewID(id.clientSeq, id.lamport+1, id.actor)
}

func (id *ID) SetActor(actor *time.ActorID) *ID {
	return NewID(id.clientSeq, id.lamport, actor)
}

func (id *ID) ClientSeq() uint32 {
	return id.clientSeq
}

func (id *ID) Lamport() uint64 {
	return id.lamport
}

func (id *ID) Actor() *time.ActorID {
	return id.actor
}
