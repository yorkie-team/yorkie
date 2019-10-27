package time

import (
	"fmt"

	"github.com/google/uuid"
)

var InitialTicket = NewTicket(0, 0, nil)

type Ticket struct {
	lamport   uint64
	delimiter uint32
	actorID   *uuid.UUID
}

func (t *Ticket) Key() string {
	if t.actorID == nil {
		return fmt.Sprintf("%d:%d:", t.lamport, t.delimiter)
	}
	return fmt.Sprintf(
		"%d:%d:%s", t.lamport, t.delimiter, t.actorID.String(),
	)
}

func NewTicket(
	lamport uint64,
	delimiter uint32,
	actorID *uuid.UUID,
) *Ticket {
	return &Ticket{
		lamport:   lamport,
		delimiter: delimiter,
		actorID:   actorID,
	}
}
