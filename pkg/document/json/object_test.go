package json_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/yorkie-team/yorkie/pkg/document/json"
	"github.com/yorkie-team/yorkie/testhelper"
)

func TestObject(t *testing.T) {
	t.Run("marshal test", func(t *testing.T) {
		root := testhelper.TestRoot()
		ctx := testhelper.TextChangeContext(root)

		obj := json.NewObject(json.NewRHTPriorityQueueMap(), ctx.IssueTimeTicket())

		obj.Set("k1", json.NewPrimitive("v1", ctx.IssueTimeTicket()))
		assert.Equal(t, `{"k1":"v1"}`, obj.Marshal())
		obj.Set("k2", json.NewPrimitive("v2", ctx.IssueTimeTicket()))
		assert.Equal(t, `{"k1":"v1","k2":"v2"}`, obj.Marshal())
		obj.Delete("k1", ctx.IssueTimeTicket())
		assert.Equal(t, `{"k2":"v2"}`, obj.Marshal())
	})
}
