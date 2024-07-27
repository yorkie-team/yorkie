package crdt_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/yorkie-team/yorkie/pkg/document/crdt"
	"github.com/yorkie-team/yorkie/test/helper"
)

func TestRHT_DeepCopy(t *testing.T) {
	root := helper.TestRoot()
	ctx := helper.TextChangeContext(root)

	rht := crdt.NewRHT()
	rht.Set("key1", "value1", ctx.IssueTimeTicket())
	rht.Remove("key2", ctx.IssueTimeTicket())

	rht2 := rht.DeepCopy()
	assert.Equal(t, rht.Marshal(), rht2.Marshal())
	assert.Equal(t, len(rht.Nodes()), len(rht2.Nodes()))
}

func TestRHT_Marshal(t *testing.T) {
	tests := []struct {
		desc      string
		insertKey string
		insertVal string
		expectStr string
	}{
		{
			desc:      `1. empty hash table`,
			insertKey: ``,
			insertVal: ``,
			expectStr: `{}`,
		},
		{
			desc:      `2. only one element`,
			insertKey: "hello\\\\\\t",
			insertVal: "world\"\f\b",
			expectStr: `{"hello\\\\\\t":"world\"\f\b"}`,
		},
		{
			desc:      `3. non-empty hash table`,
			insertKey: "hi",
			insertVal: `test\r`,
			expectStr: `{"hello\\\\\\t":"world\"\f\b","hi":"test\\r"}`,
		},
	}

	root := helper.TestRoot()
	ctx := helper.TextChangeContext(root)

	rht := crdt.NewRHT()

	for _, tt := range tests {
		t.Run(tt.desc, func(t *testing.T) {
			if len(tt.insertKey) > 0 {
				rht.Set(tt.insertKey, tt.insertVal, ctx.IssueTimeTicket())
			}
			assert.Equal(t, tt.expectStr, rht.Marshal())
		})
	}
}

func TestRHT_Set(t *testing.T) {
	key1, val1 := `key1`, `value1`
	key2, val2 := `key2`, `value2`

	tests := []struct {
		desc       string
		insertKey  []string
		insertVal  []string
		expectStr  string
		expectSize int
	}{
		{
			desc:       `1. set elements`,
			insertKey:  []string{key1, key2},
			insertVal:  []string{val1, val2},
			expectStr:  `{"key1":"value1","key2":"value2"}`,
			expectSize: 2,
		},
		{
			desc:       `2. change elements`,
			insertKey:  []string{key1, key2},
			insertVal:  []string{val2, val1},
			expectStr:  `{"key1":"value2","key2":"value1"}`,
			expectSize: 2,
		},
	}

	root := helper.TestRoot()
	ctx := helper.TextChangeContext(root)

	rht := crdt.NewRHT()

	for _, tt := range tests {
		t.Run(tt.desc, func(t *testing.T) {
			for i, key := range tt.insertKey {
				rht.Set(key, tt.insertVal[i], ctx.IssueTimeTicket())
			}
			assert.Equal(t, tt.expectStr, rht.Marshal())
			assert.Equal(t, tt.expectSize, rht.Len())
		})
	}
}

func TestRHT_Remove(t *testing.T) {
	key1, val1, val11 := `key1`, `value1`, `value11`
	key2, val2, val22 := `key2`, `value2`, `value22`

	tests := []struct {
		desc            string
		insertKey       []string
		insertVal       []string
		deleteKey       []string
		deleteVal       []string
		expectXML       string
		expectJSON      string
		expectTotalSize int
		expectSize      int
	}{
		{
			desc:            `1. set elements`,
			insertKey:       []string{key1, key2},
			insertVal:       []string{val1, val2},
			deleteKey:       []string{},
			deleteVal:       []string{},
			expectXML:       `key1="value1" key2="value2"`,
			expectJSON:      `{"key1":"value1","key2":"value2"}`,
			expectTotalSize: 2,
			expectSize:      2,
		},
		{
			desc:            `2. remove element`,
			insertKey:       []string{},
			insertVal:       []string{},
			deleteKey:       []string{key1},
			deleteVal:       []string{val1},
			expectXML:       `key2="value2"`,
			expectJSON:      `{"key2":"value2"}`,
			expectTotalSize: 2,
			expectSize:      1,
		},
		{
			desc:            `3. set after remove`,
			insertKey:       []string{key1},
			insertVal:       []string{val11},
			deleteKey:       []string{},
			deleteVal:       []string{},
			expectXML:       `key1="value11" key2="value2"`,
			expectJSON:      `{"key1":"value11","key2":"value2"}`,
			expectTotalSize: 2,
			expectSize:      2,
		},
		{
			desc:            `4. remove element`,
			insertKey:       []string{key2},
			insertVal:       []string{val22},
			deleteKey:       []string{key1},
			deleteVal:       []string{val11},
			expectXML:       `key2="value22"`,
			expectJSON:      `{"key2":"value22"}`,
			expectTotalSize: 2,
			expectSize:      1,
		},
		{
			desc:            `5. remove element again`,
			insertKey:       []string{},
			insertVal:       []string{},
			deleteKey:       []string{key1},
			deleteVal:       []string{val11},
			expectXML:       `key2="value22"`,
			expectJSON:      `{"key2":"value22"}`,
			expectTotalSize: 2,
			expectSize:      1,
		},
		{
			desc:            `6. remove element(cleared)`,
			insertKey:       []string{},
			insertVal:       []string{},
			deleteKey:       []string{key2},
			deleteVal:       []string{val22},
			expectXML:       ``,
			expectJSON:      `{}`,
			expectTotalSize: 2,
			expectSize:      0,
		},
		{
			desc:            `7. remove not exist key`,
			insertKey:       []string{},
			insertVal:       []string{},
			deleteKey:       []string{`not-exist-key`},
			deleteVal:       []string{``},
			expectXML:       ``,
			expectJSON:      `{}`,
			expectTotalSize: 3,
			expectSize:      0,
		},
	}

	root := helper.TestRoot()
	ctx := helper.TextChangeContext(root)

	rht := crdt.NewRHT()

	for _, tt := range tests {
		t.Run(tt.desc, func(t *testing.T) {
			for i, key := range tt.insertKey {
				rht.Set(key, tt.insertVal[i], ctx.IssueTimeTicket())
			}
			for i, key := range tt.deleteKey {
				nodes := rht.Remove(key, ctx.IssueTimeTicket())
				assert.Equal(t, tt.deleteVal[i], nodes[len(nodes)-1].Value())
			}
			assert.Equal(t, tt.expectJSON, rht.Marshal())
			assert.Equal(t, tt.expectSize, rht.Len())
			assert.Equal(t, tt.expectTotalSize, len(rht.Nodes()))
			assert.Equal(t, tt.expectSize, len(rht.Elements()))
		})
	}
}
