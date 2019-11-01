package time

import (
	"fmt"
)

var InitialTicket = NewTicket(0, 0, nil)

type Ticket struct {
	lamport   uint64
	delimiter uint32
	actorID   *ActorID
}

func (t *Ticket) Key() string {
	if t.actorID == nil {
		return fmt.Sprintf("%d:%d:", t.lamport, t.delimiter)
	}

	return fmt.Sprintf(
		"%d:%d:%s", t.lamport, t.delimiter, t.actorID.String(),
	)
}

func (t *Ticket) Lamport() uint64 {
	return t.lamport
}

func (t *Ticket) Delimiter() uint32 {
	return t.delimiter
}

func (t *Ticket) ActorID() *ActorID {
	return t.actorID
}

func (t *Ticket) CompareTo(other *Ticket) int {
	if t.lamport > other.lamport {
		return 1
	} else if t.lamport < other.lamport {
		return -1
	}

	return t.actorID.CompareTo(other.ActorID())
}

func NewTicket(
	lamport uint64,
	delimiter uint32,
	actorID *ActorID,
) *Ticket {
	return &Ticket{
		lamport:   lamport,
		delimiter: delimiter,
		actorID:   actorID,
	}
}
