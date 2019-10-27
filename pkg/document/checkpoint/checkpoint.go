package checkpoint

var Initial = New(0, 0)

type Checkpoint struct {
	serverSeq uint64
	clientSeq uint32
}

func New(serverSeq uint64, clientSeq uint32) *Checkpoint {
	return &Checkpoint{
		serverSeq: serverSeq,
		clientSeq: clientSeq,
	}
}

func (cp *Checkpoint) NextClientSeq() *Checkpoint {
	return New(cp.serverSeq, cp.clientSeq+1)
}

func (cp *Checkpoint) NextServerSeq() *Checkpoint {
	return New(cp.serverSeq+1, cp.clientSeq)
}
