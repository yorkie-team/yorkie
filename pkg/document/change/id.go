package change

import (
	"github.com/google/uuid"

	"github.com/hackerwins/rottie/pkg/document/time"
)

var (
	InitialID = NewID(0, 0, nil)
)

type ID struct {
	clientSeq uint32
	lamport   uint64
	actorID   *uuid.UUID
}

func NewID(
	clientSeq uint32,
	lamport uint64,
	actorID *uuid.UUID,
) *ID {
	return &ID{
		clientSeq: clientSeq,
		lamport:   lamport,
		actorID:   actorID,
	}
}

func (id *ID) Next() *ID {
	return &ID{
		clientSeq: id.clientSeq + 1,
		lamport:   id.lamport + 1,
		actorID:   id.actorID,
	}
}

func (id *ID) NewTimeTicket(delimiter uint32) *time.Ticket {
	return time.NewTicket(
		id.lamport,
		delimiter,
		id.actorID,
	)
}

func (id *ID) Sync(other *ID) *ID {
	if id.lamport < other.lamport {
		return NewID(id.clientSeq, other.lamport, id.actorID)
	}

	return NewID(id.clientSeq, id.lamport+1, id.actorID)
}
