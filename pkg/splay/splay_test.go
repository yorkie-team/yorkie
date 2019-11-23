package splay_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/hackerwins/yorkie/pkg/splay"
)

type stringValue struct {
	content string
}

func newSplayNode(content string) *splay.Node {
	return splay.NewNode(&stringValue{
		content: content,
	})
}

func (v *stringValue) Len() int {
	return len(v.content)
}

func (v *stringValue) String() string {
	return v.content
}

func TestSplayTree(t *testing.T) {
	t.Run("insert and splay test", func(t *testing.T) {
		tree := splay.NewTree()

		nodeA := tree.Insert(newSplayNode("A2"))
		assert.Equal(t, "[2,2]A2", tree.AnnotatedString())
		nodeB := tree.Insert(newSplayNode("B23"))
		assert.Equal(t, "[2,2]A2[5,3]B23", tree.AnnotatedString())
		nodeC := tree.Insert(newSplayNode("C234"))
		assert.Equal(t, "[2,2]A2[5,3]B23[9,4]C234", tree.AnnotatedString())
		nodeD := tree.Insert(newSplayNode("D2345"))
		assert.Equal(t, "[2,2]A2[5,3]B23[9,4]C234[14,5]D2345", tree.AnnotatedString())

		tree.Splay(nodeB)
		assert.Equal(t, "[2,2]A2[14,3]B23[9,4]C234[5,5]D2345", tree.AnnotatedString())

		assert.Equal(t, tree.IndexOf(nodeA), 0)
		assert.Equal(t, tree.IndexOf(nodeB), 2)
		assert.Equal(t, tree.IndexOf(nodeC), 5)
		assert.Equal(t, tree.IndexOf(nodeD), 9)
	})
}
