package crdt_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/yorkie-team/yorkie/pkg/document/crdt"
	"github.com/yorkie-team/yorkie/test/helper"
)

func TestRHT(t *testing.T) {
	t.Run("marshal test", func(t *testing.T) {
		jsonTests := []struct {
			insertKey string
			insertVal string
			expectStr string
		}{
			{ // 1. empty hash table
				insertKey: ``,
				insertVal: ``,
				expectStr: `{}`,
			},
			{ // 2. only one element
				insertKey: "hello\\\\\\t",
				insertVal: "world\"\f\b",
				expectStr: `{"hello\\\\\\t":"world\"\f\b"}`,
			},
			{ // 3. non-empty hash table
				insertKey: "hi",
				insertVal: `test\r`,
				expectStr: `{"hello\\\\\\t":"world\"\f\b","hi":"test\\r"}`,
			},
		}

		rht := crdt.NewRHT()

		for _, tt := range jsonTests {
			if len(tt.insertKey) > 0 {
				rht.Set(tt.insertKey, tt.insertVal, nil)
			}
			assert.Equal(t, tt.expectStr, rht.Marshal())
		}
	})

	t.Run("xml test", func(t *testing.T) {
		xmlTests := []struct {
			insertKey string
			insertVal string
			expectStr string
		}{
			{ // 1. empty hash table
				insertKey: ``,
				insertVal: ``,
				expectStr: ``,
			},
			{ // 2. only one element
				insertKey: "hello\\\\\\t",
				insertVal: "world\"\f\b",
				expectStr: `hello\\\t="world\"\f\b"`,
			},
			{ // 3. non-empty hash table
				insertKey: "hi",
				insertVal: `test\r`,
				expectStr: `hello\\\t="world\"\f\b" hi="test\\r"`,
			},
		}

		rht := crdt.NewRHT()

		for _, tt := range xmlTests {
			if len(tt.insertKey) > 0 {
				rht.Set(tt.insertKey, tt.insertVal, nil)
			}
			assert.Equal(t, tt.expectStr, rht.ToXML())
		}
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
		key1 := `key1`
		val1 := `value1`
		val11 := `value11`

		key2 := `key2`
		val2 := `value2`
		val22 := `value22`

		removeTests := []struct {
			insertKey  []string
			insertVal  []string
			deleteKey  []string
			deleteVal  []string
			expectXML  string
			expectJSON string
			expectSize int
		}{
			{ // 1. set elements
				insertKey:  []string{key1, key2},
				insertVal:  []string{val1, val2},
				deleteKey:  []string{},
				deleteVal:  []string{},
				expectXML:  `key1="value1" key2="value2"`,
				expectJSON: `{"key1":"value1","key2":"value2"}`,
				expectSize: 2,
			},
			{ // 2. remove element
				insertKey:  []string{},
				insertVal:  []string{},
				deleteKey:  []string{key1},
				deleteVal:  []string{val1},
				expectXML:  `key2="value2"`,
				expectJSON: `{"key2":"value2"}`,
				expectSize: 1,
			},
			{ // 3. set after remove
				insertKey:  []string{key1},
				insertVal:  []string{val11},
				deleteKey:  []string{},
				deleteVal:  []string{},
				expectXML:  `key1="value11" key2="value2"`,
				expectJSON: `{"key1":"value11","key2":"value2"}`,
				expectSize: 2,
			},
			{ // 4. remove element
				insertKey:  []string{key2},
				insertVal:  []string{val22},
				deleteKey:  []string{key1},
				deleteVal:  []string{val11},
				expectXML:  `key2="value22"`,
				expectJSON: `{"key2":"value22"}`,
				expectSize: 1,
			},
			{ // 5. remove element again
				insertKey:  []string{},
				insertVal:  []string{},
				deleteKey:  []string{key1},
				deleteVal:  []string{``},
				expectXML:  `key2="value22"`,
				expectJSON: `{"key2":"value22"}`,
				expectSize: 1,
			},
			{ // 6. remove element -> clear
				insertKey:  []string{},
				insertVal:  []string{},
				deleteKey:  []string{key2},
				deleteVal:  []string{val22},
				expectXML:  ``,
				expectJSON: `{}`,
				expectSize: 0,
			},
			{ // 7. remove not exist key
				insertKey:  []string{},
				insertVal:  []string{},
				deleteKey:  []string{`not-exist-key`},
				deleteVal:  []string{``},
				expectXML:  ``,
				expectJSON: `{}`,
				expectSize: 0,
			},
		}

		root := helper.TestRoot()
		ctx := helper.TextChangeContext(root)

		rht := crdt.NewRHT()

		for _, tt := range removeTests {
			for i, key := range tt.insertKey {
				rht.Set(key, tt.insertVal[i], ctx.IssueTimeTicket())
			}
			for i, key := range tt.deleteKey {
				removedElement := rht.Remove(key, ctx.IssueTimeTicket())
				assert.Equal(t, tt.deleteVal[i], removedElement)
			}
			assert.Equal(t, tt.expectXML, rht.ToXML())
			assert.Equal(t, tt.expectJSON, rht.Marshal())
			assert.Equal(t, tt.expectSize, rht.Len())
			assert.Equal(t, tt.expectSize, len(rht.Nodes()))
			assert.Equal(t, tt.expectSize, len(rht.Elements()))
		}
	})
}
