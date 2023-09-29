package crdt_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/yorkie-team/yorkie/pkg/document/crdt"
	"github.com/yorkie-team/yorkie/test/helper"
)

func TestRGATreeList(t *testing.T) {
	t.Run("rga_tree_list operations test", func(t *testing.T) {
		root := helper.TestRoot()
		ctx := helper.TextChangeContext(root)

		elements := crdt.NewRGATreeList()
		for _, v := range []string{"1", "2", "3"} {
			primitive, err := crdt.NewPrimitive(v, ctx.IssueTimeTicket())
			assert.NoError(t, err)
			err = elements.Add(primitive)
			assert.NoError(t, err)
		}
		assert.Equal(t, `["1","2","3"]`, elements.Marshal())

		nodes := elements.Nodes()
		assert.Equal(t, len(nodes), 3)

		targetElement, err := elements.Get(1)
		assert.NoError(t, err)
		assert.Equal(t, `"2"`, targetElement.Element().Marshal())

		prevCreatedAt, err := elements.FindPrevCreatedAt(targetElement.CreatedAt())
		assert.NoError(t, err)
		assert.Equal(t, prevCreatedAt.Compare(targetElement.CreatedAt()), -1)

		err = elements.MoveAfter(targetElement.CreatedAt(), prevCreatedAt, ctx.IssueTimeTicket())
		assert.NoError(t, err)
		assert.Equal(t, `["2","1","3"]`, elements.Marshal())

		_, err = elements.DeleteByCreatedAt(targetElement.CreatedAt(), ctx.IssueTimeTicket())
		assert.NoError(t, err)
		assert.Equal(t, `["1","3"]`, elements.Marshal())

		_, err = elements.Delete(1, ctx.IssueTimeTicket())
		assert.NoError(t, err)
		assert.Equal(t, `["1"]`, elements.Marshal())

	})

	t.Run("invalid createdAt test", func(t *testing.T) {
		root := helper.TestRoot()
		ctx := helper.TextChangeContext(root)

		validCreatedAt, invalidCreatedAt := ctx.IssueTimeTicket(), ctx.IssueTimeTicket()
		elements := crdt.NewRGATreeList()
		primitive, err := crdt.NewPrimitive("1", validCreatedAt)
		assert.NoError(t, err)
		err = elements.Add(primitive)
		assert.NoError(t, err)

		_, err = elements.DeleteByCreatedAt(invalidCreatedAt, ctx.IssueTimeTicket())
		assert.ErrorIs(t, err, crdt.ErrChildNotFound)

		err = elements.MoveAfter(validCreatedAt, invalidCreatedAt, ctx.IssueTimeTicket())
		assert.ErrorIs(t, err, crdt.ErrChildNotFound)

		err = elements.MoveAfter(invalidCreatedAt, validCreatedAt, ctx.IssueTimeTicket())
		assert.ErrorIs(t, err, crdt.ErrChildNotFound)

		_, err = elements.FindPrevCreatedAt(invalidCreatedAt)
		assert.ErrorIs(t, err, crdt.ErrChildNotFound)
	})
}
