package checkpoint

import (
	"fmt"
)

// Initial is the initial value of the checkpoint.
var Initial = New(0, 0)

// Checkpoint is used to determine the client received changes.
type Checkpoint struct {
	ServerSeq uint64
	ClientSeq uint32
}

// New creates a new instance of Checkpoint.
func New(serverSeq uint64, clientSeq uint32) *Checkpoint {
	return &Checkpoint{
		ServerSeq: serverSeq,
		ClientSeq: clientSeq,
	}
}

func (cp *Checkpoint) NextClientSeq() *Checkpoint {
	return New(cp.ServerSeq, cp.ClientSeq+1)
}

func (cp *Checkpoint) NextServerSeq(serverSeq uint64) *Checkpoint {
	if cp.ServerSeq == serverSeq {
		return cp
	}

	return New(serverSeq, cp.ClientSeq)
}

func (cp *Checkpoint) IncreaseClientSeq(inc uint32) *Checkpoint {
	if inc == 0 {
		return cp
	}
	return New(cp.ServerSeq, cp.ClientSeq+inc)
}

func (cp *Checkpoint) SyncClientSeq(clientSeq uint32) *Checkpoint {
	if cp.ClientSeq < clientSeq {
		return New(cp.ServerSeq, clientSeq)
	}

	return cp
}

func (cp *Checkpoint) Forward(other *Checkpoint) *Checkpoint {
	if cp.Equals(other) {
		return cp
	}

	maxServerSeq := cp.ServerSeq
	if cp.ServerSeq < other.ServerSeq {
		maxServerSeq = other.ServerSeq
	}

	maxClientSeq := cp.ClientSeq
	if cp.ClientSeq < other.ClientSeq {
		maxClientSeq = other.ClientSeq
	}

	return New(maxServerSeq, maxClientSeq)
}

// Equals returns whether the given checkpoint is equal to this checkpoint or not.
func (cp *Checkpoint) Equals(other *Checkpoint) bool {
	return cp.ServerSeq == other.ServerSeq &&
		cp.ClientSeq == other.ClientSeq
}

// String returns the string of infomation about this checkpoint.
func (cp *Checkpoint) String() string {
	return fmt.Sprintf("serverSeq=%d, clientSeq=%d", cp.ServerSeq, cp.ClientSeq)
}
