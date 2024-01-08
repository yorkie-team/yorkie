package crdt_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/yorkie-team/yorkie/pkg/document/crdt"
	"github.com/yorkie-team/yorkie/test/helper"
)

func TestRHT(t *testing.T) {
	t.Run("marshal test", func(t *testing.T) {
		key1 := `hello\\\t`
		value1 := "world\"\f\b"
		key2 := "hi"
		value2 := `test\r`
		expected1 := `{}`
		expected2 := `{"hello\\\\\\t":"world\"\f\b"}`
		expected3 := `{"hello\\\\\\t":"world\"\f\b","hi":"test\\r"}`

		// 1. empty hash table
		rht := crdt.NewRHT()
		actual1 := rht.Marshal()
		assert.Equal(t, expected1, actual1)

		// 2. only one element
		rht.Set(key1, value1, nil)
		actual2 := rht.Marshal()
		assert.Equal(t, expected2, actual2)

		// 3. non-empty hash table
		rht.Set(key2, value2, nil)
		actual3 := rht.Marshal()
		assert.Equal(t, expected3, actual3)
	})

	t.Run("xml test", func(t *testing.T) {
		key1 := `hello\\\t`
		value1 := "world\"\f\b"
		key2 := "hi"
		value2 := `test\r`
		expected1 := ``
		expected2 := `hello\\\t="world\"\f\b"`
		expected3 := `hello\\\t="world\"\f\b" hi="test\\r"`

		// 1. empty hash table
		rht := crdt.NewRHT()
		actual1 := rht.ToXML()
		assert.Equal(t, expected1, actual1)

		// 2. only one element
		rht.Set(key1, value1, nil)
		actual2 := rht.ToXML()
		assert.Equal(t, expected2, actual2)

		// 3. non-empty hash table
		rht.Set(key2, value2, nil)
		actual3 := rht.ToXML()
		assert.Equal(t, expected3, actual3)
	})

	t.Run("change value test", func(t *testing.T) {
		root := helper.TestRoot()
		ctx := helper.TextChangeContext(root)

		rht := crdt.NewRHT()
		key1 := `key1`
		value1 := `value1`
		key2 := `key2`
		value2 := `value2`

		// 1. set elements
		rht.Set(key1, value1, ctx.IssueTimeTicket())
		rht.Set(key2, value2, ctx.IssueTimeTicket())
		expected1 := `{"key1":"value1","key2":"value2"}`
		actual1 := rht.Marshal()
		assert.Equal(t, expected1, actual1)
		assert.Equal(t, 2, rht.Len())

		// 2. change elements
		rht.Set(key1, value2, ctx.IssueTimeTicket())
		rht.Set(key2, value1, ctx.IssueTimeTicket())
		expected2 := `{"key1":"value2","key2":"value1"}`
		actual2 := rht.Marshal()
		assert.Equal(t, expected2, actual2)
		assert.Equal(t, 2, rht.Len())
	})

	t.Run("remove test", func(t *testing.T) {
		root := helper.TestRoot()
		ctx := helper.TextChangeContext(root)

		rht := crdt.NewRHT()
		key1 := `key1`
		value1 := `value1`
		key2 := `key2`
		value2 := `value2`

		value11 := `value11`
		value22 := `value22`

		// 1. set elements
		rht.Set(key1, value1, ctx.IssueTimeTicket())
		rht.Set(key2, value2, ctx.IssueTimeTicket())
		expected1 := `{"key1":"value1","key2":"value2"}`
		actual1 := rht.Marshal()
		assert.Equal(t, expected1, actual1)
		assert.Equal(t, 2, rht.Len())
		assert.Equal(t, 2, len(rht.Nodes()))
		assert.Equal(t, 2, len(rht.Elements()))

		// 2. remove element
		rht.Remove(key1, ctx.IssueTimeTicket())
		expected2 := `{"key2":"value2"}`
		actual2 := rht.Marshal()
		assert.Equal(t, expected2, actual2)
		assert.Equal(t, 1, rht.Len())
		assert.Equal(t, 1, len(rht.Nodes()))
		assert.Equal(t, 1, len(rht.Elements()))

		// 3. set after remove
		rht.Set(key1, value11, ctx.IssueTimeTicket())
		expected3 := `{"key1":"value11","key2":"value2"}`
		actual3 := rht.Marshal()
		assert.Equal(t, expected3, actual3)
		assert.Equal(t, 2, rht.Len())
		assert.Equal(t, 2, len(rht.Nodes()))
		assert.Equal(t, 2, len(rht.Elements()))
		assert.Equal(t, `key1="value11" key2="value2"`, rht.ToXML())

		// 4. remove element
		rht.Remove(key1, ctx.IssueTimeTicket())
		rht.Set(key2, value22, ctx.IssueTimeTicket())
		expected4 := `{"key2":"value22"}`
		actual4 := rht.Marshal()
		assert.Equal(t, expected4, actual4)
		assert.Equal(t, 1, rht.Len())
		assert.Equal(t, 1, len(rht.Nodes()))
		assert.Equal(t, 1, len(rht.Elements()))

		// 5. remove element again
		assert.Equal(t, ``, rht.Remove(key1, ctx.IssueTimeTicket()))
		expected5 := `{"key2":"value22"}`
		actual5 := rht.Marshal()
		assert.Equal(t, expected5, actual5)
		assert.Equal(t, 1, rht.Len())
		assert.Equal(t, 1, len(rht.Nodes()))
		assert.Equal(t, 1, len(rht.Elements()))
		assert.Equal(t, `key2="value22"`, rht.ToXML())

		// 6. remove element -> clear
		rht.Remove(key2, ctx.IssueTimeTicket())
		expected6 := `{}`
		actual6 := rht.Marshal()
		assert.Equal(t, expected6, actual6)
		assert.Equal(t, 0, rht.Len())
		assert.Equal(t, 0, len(rht.Nodes()))
		assert.Equal(t, 0, len(rht.Elements()))
		assert.Equal(t, ``, rht.ToXML())

		// 7. remove not exist key
		assert.Equal(t, ``, rht.Remove(`key3`, ctx.IssueTimeTicket()))
		expected7 := `{}`
		actual7 := rht.Marshal()
		assert.Equal(t, expected7, actual7)
		assert.Equal(t, 0, rht.Len())
		assert.Equal(t, ``, rht.ToXML())
		assert.Equal(t, 0, len(rht.Nodes()))
		assert.Equal(t, 0, len(rht.Elements()))
	})
}
