package change

import (
	"github.com/yorkie-team/yorkie/pkg/document/checkpoint"
	"github.com/yorkie-team/yorkie/pkg/document/key"
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

// HasChanges returns the whether pack has changes or not.
func (p *Pack) HasChanges() bool {
	return len(p.Changes) > 0
}
