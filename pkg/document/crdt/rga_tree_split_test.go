package crdt_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/yorkie-team/yorkie/pkg/document/crdt"
	"github.com/yorkie-team/yorkie/pkg/document/time"
)

func TestRGATreeSplit(t *testing.T) {
	t.Run("compare nil id panic test", func(t *testing.T) {
		id := crdt.NewRGATreeSplitNodeID(time.InitialTicket, 0)
		compare, err := id.Compare(nil)
		assert.Equal(t, compare, 0)
		assert.Error(t, err)
	})
}
