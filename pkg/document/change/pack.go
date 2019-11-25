package change

import (
	"github.com/hackerwins/yorkie/pkg/document/checkpoint"
	"github.com/hackerwins/yorkie/pkg/document/key"
)

// Pack is a unit for delivering changes in a document to the remote.
type Pack struct {
	DocumentKey *key.Key
	Checkpoint  *checkpoint.Checkpoint
	Changes     []*Change
}

// NewPack creates a new instance of Pack.
func NewPack(
	key *key.Key,
	cp *checkpoint.Checkpoint,
	changes []*Change,
) *Pack {
	return &Pack{
		DocumentKey: key,
		Checkpoint:  cp,
		Changes:     changes,
	}
}
