package json_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/yorkie-team/yorkie/pkg/document/json"
	"github.com/yorkie-team/yorkie/pkg/document/time"
)

func TestRGATreeSplit(t *testing.T) {
	t.Run("compare nil id panic test", func(t *testing.T) {
		id := json.NewRGATreeSplitNodeID(time.InitialTicket, 0)
		assert.Panics(t, func() { id.Compare(nil) }, "ID cannot be null")
	})
}
