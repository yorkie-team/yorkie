package datatype_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/hackerwins/yorkie/pkg/document/json/datatype"
)

type stringValue struct {
	content string
}

func newSplayNode(content string) *datatype.SplayNode {
	return datatype.NewSplayNode(&stringValue{
		content: content,
	})
}

func (v *stringValue) GetLength() int {
	return len(v.content)
}

func (v *stringValue) String() string {
	return v.content
}

func TestSplayTree(t *testing.T) {
	t.Run("insert and splay test", func(t *testing.T) {
		nodeA := newSplayNode("A2")
		tree := datatype.NewSplayTree(nodeA)
		assert.Equal(t, "[2,2]A2", tree.MetaString())
		nodeB := tree.Insert(newSplayNode("B23"))
		assert.Equal(t, "[2,2]A2[5,3]B23", tree.MetaString())
		nodeC := tree.Insert(newSplayNode("C234"))
		assert.Equal(t, "[2,2]A2[5,3]B23[9,4]C234", tree.MetaString())
		nodeD := tree.Insert(newSplayNode("D2345"))
		assert.Equal(t, "[2,2]A2[5,3]B23[9,4]C234[14,5]D2345", tree.MetaString())

		tree.Splay(nodeB)

		assert.Equal(t, tree.IndexOf(nodeA), 0)
		assert.Equal(t, tree.IndexOf(nodeB), 2)
		assert.Equal(t, tree.IndexOf(nodeC), 5)
		assert.Equal(t, tree.IndexOf(nodeD), 9)
	})
}
