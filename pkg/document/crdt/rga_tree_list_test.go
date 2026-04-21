package crdt_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/yorkie-team/yorkie/pkg/document/crdt"
	"github.com/yorkie-team/yorkie/pkg/document/time"
	"github.com/yorkie-team/yorkie/test/helper"
)

// buildList creates an RGATreeList with elements whose createdAt tickets match
// the provided tickets. Returns the list and the createdAt tickets of the elements.
func buildList(t *testing.T, values []string, tickets []*time.Ticket) *crdt.RGATreeList {
	t.Helper()
	elements := crdt.NewRGATreeList()
	for i, v := range values {
		primitive, err := crdt.NewPrimitive(v, tickets[i])
		assert.NoError(t, err)
		err = elements.Add(primitive)
		assert.NoError(t, err)
	}
	return elements
}

func TestRGATreeListMoveAfterLWW(t *testing.T) {
	t.Run("concurrent moves of the same element, both orders converge", func(t *testing.T) {
		// Initial: [A, B, C]
		// Op1 @t4: move(A, after B) — move A to after B
		// Op2 @t5: move(A, after C) — move A to after C
		// t5 > t4, so Op2 wins regardless of application order.
		// Expected final: [B, C, A]

		root := helper.TestRoot()
		ctx := helper.TextChangeContext(root)

		// Issue tickets for elements.
		tA := ctx.IssueTimeTicket() // t1
		tB := ctx.IssueTimeTicket() // t2
		tC := ctx.IssueTimeTicket() // t3

		// Issue tickets for move operations.
		tMove1 := ctx.IssueTimeTicket() // t4 (move A after B)
		tMove2 := ctx.IssueTimeTicket() // t5 (move A after C), wins by LWW

		// Order 1: Op1 then Op2
		list1 := buildList(t, []string{"A", "B", "C"}, []*time.Ticket{tA, tB, tC})
		_, err := list1.MoveAfter(tB, tA, tMove1)
		assert.NoError(t, err)
		assert.Equal(t, `["B","A","C"]`, list1.Marshal())
		_, err = list1.MoveAfter(tC, tA, tMove2)
		assert.NoError(t, err)
		result1 := list1.Marshal()

		// Order 2: Op2 then Op1
		list2 := buildList(t, []string{"A", "B", "C"}, []*time.Ticket{tA, tB, tC})
		_, err = list2.MoveAfter(tC, tA, tMove2)
		assert.NoError(t, err)
		_, err = list2.MoveAfter(tB, tA, tMove1) // should be discarded (LWW)
		assert.NoError(t, err)
		result2 := list2.Marshal()

		// Both orders must produce the same result.
		assert.Equal(t, result1, result2)
		assert.Equal(t, `["B","C","A"]`, result1)
	})
}

func TestRGATreeListMoveAfterConvergence(t *testing.T) {
	t.Run("two moves of different elements converge in any order", func(t *testing.T) {

		// Initial: [A, B, C]
		// Op1 @t4: move(A, after C) → [B, C, A]
		// Op2 @t5: move(B, after A) → B follows A wherever A is
		// Since t5 > t4, Op2 sees A's position.

		root := helper.TestRoot()
		ctx := helper.TextChangeContext(root)

		tA := ctx.IssueTimeTicket()
		tB := ctx.IssueTimeTicket()
		tC := ctx.IssueTimeTicket()

		tOp1 := ctx.IssueTimeTicket() // t4: move(A, after C)
		tOp2 := ctx.IssueTimeTicket() // t5: move(B, after A)

		// Order 1: Op1 → Op2
		list1 := buildList(t, []string{"A", "B", "C"}, []*time.Ticket{tA, tB, tC})
		_, err := list1.MoveAfter(tC, tA, tOp1)
		assert.NoError(t, err)
		_, err = list1.MoveAfter(tA, tB, tOp2)
		assert.NoError(t, err)
		result1 := list1.Marshal()

		// Order 2: Op2 → Op1
		list2 := buildList(t, []string{"A", "B", "C"}, []*time.Ticket{tA, tB, tC})
		_, err = list2.MoveAfter(tA, tB, tOp2)
		assert.NoError(t, err)
		_, err = list2.MoveAfter(tC, tA, tOp1)
		assert.NoError(t, err)
		result2 := list2.Marshal()

		// Both orders must converge.
		assert.Equal(t, result1, result2, "Diverged: %s != %s", result1, result2)
	})

	t.Run("three concurrent moves converge in any order", func(t *testing.T) {

		// Initial: [A, B, C, D]
		// Op1 @t5: move(A, after D)
		// Op2 @t6: move(B, after A)
		// Op3 @t7: move(C, after B)
		// These form a chain of dependencies.

		root := helper.TestRoot()
		ctx := helper.TextChangeContext(root)

		tA := ctx.IssueTimeTicket()
		tB := ctx.IssueTimeTicket()
		tC := ctx.IssueTimeTicket()
		tD := ctx.IssueTimeTicket()

		tOp1 := ctx.IssueTimeTicket()
		tOp2 := ctx.IssueTimeTicket()
		tOp3 := ctx.IssueTimeTicket()

		applyOps := func(order []int) string {
			list := buildList(t, []string{"A", "B", "C", "D"}, []*time.Ticket{tA, tB, tC, tD})
			for _, op := range order {
				var err error
				switch op {
				case 1:
					_, err = list.MoveAfter(tD, tA, tOp1)
				case 2:
					_, err = list.MoveAfter(tA, tB, tOp2)
				case 3:
					_, err = list.MoveAfter(tB, tC, tOp3)
				}
				assert.NoError(t, err)
			}
			return list.Marshal()
		}

		// Try all 6 permutations of {1,2,3}.
		orders := [][]int{
			{1, 2, 3},
			{1, 3, 2},
			{2, 1, 3},
			{2, 3, 1},
			{3, 1, 2},
			{3, 2, 1},
		}

		results := make([]string, len(orders))
		for i, order := range orders {
			results[i] = applyOps(order)
		}

		for i := 1; i < len(results); i++ {
			assert.Equal(t, results[0], results[i],
				"Order %v vs %v diverged: %s != %s", orders[0], orders[i], results[0], results[i])
		}
	})

	t.Run("concurrent moves to independent destinations converge", func(t *testing.T) {
		// Initial: [A, B, C, D]
		// Op1 @t5: move(A, after D) — destination D is not moved
		// Op2 @t6: move(B, after C) — destination C is not moved
		// Destinations are stable (not moved), so order should not matter.

		root := helper.TestRoot()
		ctx := helper.TextChangeContext(root)

		tA := ctx.IssueTimeTicket()
		tB := ctx.IssueTimeTicket()
		tC := ctx.IssueTimeTicket()
		tD := ctx.IssueTimeTicket()

		tOp1 := ctx.IssueTimeTicket() // move(A, after D)
		tOp2 := ctx.IssueTimeTicket() // move(B, after C)

		// Order 1: Op1 → Op2
		list1 := buildList(t, []string{"A", "B", "C", "D"}, []*time.Ticket{tA, tB, tC, tD})
		_, err := list1.MoveAfter(tD, tA, tOp1)
		assert.NoError(t, err)
		_, err = list1.MoveAfter(tC, tB, tOp2)
		assert.NoError(t, err)
		result1 := list1.Marshal()

		// Order 2: Op2 → Op1
		list2 := buildList(t, []string{"A", "B", "C", "D"}, []*time.Ticket{tA, tB, tC, tD})
		_, err = list2.MoveAfter(tC, tB, tOp2)
		assert.NoError(t, err)
		_, err = list2.MoveAfter(tD, tA, tOp1)
		assert.NoError(t, err)
		result2 := list2.Marshal()

		assert.Equal(t, result1, result2, "Diverged: %s != %s", result1, result2)
		assert.Equal(t, `["C","B","D","A"]`, result1)
	})
}

func TestRGATreeListMoveAfterWithDelete(t *testing.T) {
	t.Run("concurrent move and delete of the same element", func(t *testing.T) {
		// Initial: [A, B, C]
		// Op1 @t4: move(B, after C)
		// Op2 @t5: delete(B)
		// After both applied: B is moved but also deleted, so it should not appear.
		// Expected: [A, C]

		root := helper.TestRoot()
		ctx := helper.TextChangeContext(root)

		tA := ctx.IssueTimeTicket()
		tB := ctx.IssueTimeTicket()
		tC := ctx.IssueTimeTicket()

		tMove := ctx.IssueTimeTicket() // t4
		tDel := ctx.IssueTimeTicket()  // t5

		// Order 1: move then delete
		list1 := buildList(t, []string{"A", "B", "C"}, []*time.Ticket{tA, tB, tC})
		_, err := list1.MoveAfter(tC, tB, tMove)
		assert.NoError(t, err)
		assert.Equal(t, `["A","C","B"]`, list1.Marshal())
		_, err = list1.DeleteByCreatedAt(tB, tDel)
		assert.NoError(t, err)
		result1 := list1.Marshal()

		// Order 2: delete then move
		list2 := buildList(t, []string{"A", "B", "C"}, []*time.Ticket{tA, tB, tC})
		_, err = list2.DeleteByCreatedAt(tB, tDel)
		assert.NoError(t, err)
		assert.Equal(t, `["A","C"]`, list2.Marshal())
		_, err = list2.MoveAfter(tC, tB, tMove)
		assert.NoError(t, err)
		result2 := list2.Marshal()

		// Both orders should converge: B is deleted regardless of move.
		assert.Equal(t, result1, result2)
		assert.Equal(t, `["A","C"]`, result1)
	})
}

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

		_, err = elements.MoveAfter(targetElement.CreatedAt(), prevCreatedAt, ctx.IssueTimeTicket())
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

		_, err = elements.MoveAfter(validCreatedAt, invalidCreatedAt, ctx.IssueTimeTicket())
		assert.ErrorIs(t, err, crdt.ErrChildNotFound)

		_, err = elements.MoveAfter(invalidCreatedAt, validCreatedAt, ctx.IssueTimeTicket())
		assert.ErrorIs(t, err, crdt.ErrChildNotFound)

		_, err = elements.FindPrevCreatedAt(invalidCreatedAt)
		assert.ErrorIs(t, err, crdt.ErrChildNotFound)
	})
}
