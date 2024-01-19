//go:build integration

/*
 * Copyright 2024 The Yorkie Authors. All rights reserved.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package integration

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/yorkie-team/yorkie/pkg/document"
	"github.com/yorkie-team/yorkie/pkg/document/json"
	"github.com/yorkie-team/yorkie/pkg/document/presence"
	"github.com/yorkie-team/yorkie/test/helper"
)

type rangeType struct {
	from, to int
}

type rangeWithMiddleType struct {
	from, mid, to int
}

type twoRangesType struct {
	ranges [2]rangeWithMiddleType
	desc   string
}

type rangeSelector int

const (
	RangeUnknown rangeSelector = iota
	RangeFront
	RangeMiddle
	RangeBack
	RangeAll
)

func getRange(ranges twoRangesType, selector rangeSelector, user int) rangeType {
	if selector == RangeFront {
		return rangeType{ranges.ranges[user].from, ranges.ranges[user].from}
	} else if selector == RangeMiddle {
		return rangeType{ranges.ranges[user].mid, ranges.ranges[user].mid}
	} else if selector == RangeBack {
		return rangeType{ranges.ranges[user].to, ranges.ranges[user].to}
	} else if selector == RangeAll {
		return rangeType{ranges.ranges[user].from, ranges.ranges[user].to}
	}
	return rangeType{-1, -1}
}

type styleOperationType struct {
	selector   rangeSelector
	op         styleOpCode
	key, value string
	desc       string
}

type editOperationType struct {
	selector   rangeSelector
	op         editOpCode
	content    *json.TreeNode
	splitLevel int
	desc       string
}

type styleOpCode int
type editOpCode int

const (
	StyleUndefined styleOpCode = iota
	StyleRemove
	StyleSet
)

const (
	EditUndefined editOpCode = iota
	EditUpdate
)

func makeTwoRanges(from1, mid1, to1 int, from2, mid2, to2 int, desc string) twoRangesType {
	range0 := rangeWithMiddleType{from1, mid1, to1}
	range1 := rangeWithMiddleType{from2, mid2, to2}
	return twoRangesType{[2]rangeWithMiddleType{range0, range1}, desc}
}

var rangesToTestSameTypeOperation = []twoRangesType{
	makeTwoRanges(3, -1, 6, 3, -1, 6, `equal`),        // (3, 6) - (3, 6)
	makeTwoRanges(0, -1, 9, 3, -1, 6, `contain`),      // (0, 9) - (3, 6)
	makeTwoRanges(0, -1, 6, 3, -1, 9, `intersect`),    // (0, 6) - (3, 9)
	makeTwoRanges(0, -1, 3, 3, -1, 6, `side-by-side`), // (0, 3) - (3, 6)
}

var rangesToTestMixedTypeOperation = []twoRangesType{
	makeTwoRanges(3, 3, 6, 3, -1, 6, `equal`),        // (3, 6) - (3, 6)
	makeTwoRanges(0, 3, 9, 3, -1, 6, `A contains B`), // (0, 9) - (3, 6)
	makeTwoRanges(3, 3, 6, 0, -1, 9, `B contains A`), // (0, 9) - (3, 6)
	makeTwoRanges(0, 3, 6, 3, -1, 9, `intersect`),    // (0, 6) - (3, 9)
	makeTwoRanges(0, 3, 3, 3, -1, 6, `A -> B`),       // (0, 3) - (3, 6)
	makeTwoRanges(3, 3, 6, 0, -1, 3, `B -> A`),       // (0, 3) - (3, 6)
}

func runStyleOperation(t *testing.T, doc *document.Document, user int, ranges twoRangesType, operation styleOperationType) {
	interval := getRange(ranges, operation.selector, user)
	from, to := interval.from, interval.to

	assert.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
		if operation.op == StyleRemove {
			root.GetTree("t").RemoveStyle(from, to, []string{operation.key})
		} else if operation.op == StyleSet {
			root.GetTree("t").Style(from, to, map[string]string{operation.key: operation.value})
		}
		return nil
	}))
}

func runEditOperation(t *testing.T, doc *document.Document, user int, ranges twoRangesType, operation editOperationType) {
	interval := getRange(ranges, operation.selector, user)
	from, to := interval.from, interval.to

	assert.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
		root.GetTree("t").Edit(from, to, operation.content, operation.splitLevel)
		return nil
	}))
}

func TestTreeConcurrencyStyle(t *testing.T) {
	//       0   1 2    3   4 5    6   7 8    9
	// <root> <p> a </p> <p> b </p> <p> c </p> </root>
	// 0,3 : |----------|
	// 3,6 :            |----------|
	// 6,9 :                       |----------|

	initialState := json.TreeNode{
		Type: "root",
		Children: []json.TreeNode{
			{Type: "p", Children: []json.TreeNode{{Type: "text", Value: "a"}}},
			{Type: "p", Children: []json.TreeNode{{Type: "text", Value: "b"}}},
			{Type: "p", Children: []json.TreeNode{{Type: "text", Value: "c"}}},
		},
	}
	initialXML := `<root><p>a</p><p>b</p><p>c</p></root>`

	styleOperationsToTest := []styleOperationType{
		{RangeAll, StyleRemove, "bold", "", `remove-bold`},
		{RangeAll, StyleSet, "bold", "aa", `set-bold-aa`},
		{RangeAll, StyleSet, "bold", "bb", `set-bold-bb`},
		{RangeAll, StyleRemove, "italic", "", `remove-italic`},
		{RangeAll, StyleSet, "italic", "aa", `set-italic-aa`},
		{RangeAll, StyleSet, "italic", "bb", `set-italic-bb`},
	}

	runStyleTest := func(ranges twoRangesType, op1, op2 styleOperationType) {
		clients := activeClients(t, 2)
		c1, c2 := clients[0], clients[1]
		defer deactivateAndCloseClients(t, clients)

		ctx := context.Background()
		d1 := document.New(helper.TestDocKey(t))
		assert.NoError(t, c1.Attach(ctx, d1))
		d2 := document.New(helper.TestDocKey(t))
		assert.NoError(t, c2.Attach(ctx, d2))

		assert.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetNewTree("t", &initialState)
			return nil
		}))
		assert.NoError(t, c1.Sync(ctx))
		assert.NoError(t, c2.Sync(ctx))
		assert.Equal(t, initialXML, d1.Root().GetTree("t").ToXML())
		assert.Equal(t, initialXML, d2.Root().GetTree("t").ToXML())

		runStyleOperation(t, d1, 0, ranges, op1)
		runStyleOperation(t, d2, 1, ranges, op2)
		syncClientsThenAssertEqual(t, []clientAndDocPair{{c1, d1}, {c2, d2}})
	}

	for _, interval := range rangesToTestSameTypeOperation {
		for _, op1 := range styleOperationsToTest {
			for _, op2 := range styleOperationsToTest {
				desc := "concurrently style-style test " + interval.desc + "(" + op1.desc + " " + op2.desc + ")"
				t.Run(desc, func(t *testing.T) {
					runStyleTest(interval, op1, op2)
				})
			}
		}
	}
}

func TestTreeConcurrencyEditAndStyle(t *testing.T) {
	//       0   1 2    3   4 5    6   7 8    9
	// <root> <p> a </p> <p> b </p> <p> c </p> </root>
	// 0,3 : |----------|
	// 3,6 :            |----------|
	// 6,9 :                       |----------|

	initialState := json.TreeNode{
		Type: "root",
		Children: []json.TreeNode{
			{Type: "p", Children: []json.TreeNode{{Type: "text", Value: "a"}}},
			{Type: "p", Children: []json.TreeNode{{Type: "text", Value: "b"}}},
			{Type: "p", Children: []json.TreeNode{{Type: "text", Value: "c"}}},
		},
	}
	initialXML := `<root><p>a</p><p>b</p><p>c</p></root>`

	content := &json.TreeNode{Type: "p", Attributes: map[string]string{"italic": "true"}, Children: []json.TreeNode{{Type: "text", Value: `d`}}}

	editOperationsToTest := []editOperationType{
		{RangeFront, EditUpdate, content, 0, `insertFront`},
		{RangeMiddle, EditUpdate, content, 0, `insertMiddle`},
		{RangeBack, EditUpdate, content, 0, `insertBack`},
		{RangeAll, EditUpdate, nil, 0, `erase`},
		{RangeAll, EditUpdate, content, 0, `change`},
	}

	styleOperationsToTest := []styleOperationType{
		{RangeAll, StyleRemove, "bold", "", `remove-bold`},
		{RangeAll, StyleSet, "bold", "aa", `set-bold-aa`},
	}

	runEditStyleTest := func(ranges twoRangesType, op1 editOperationType, op2 styleOperationType) bool {
		clients := activeClients(t, 2)
		c1, c2 := clients[0], clients[1]
		defer deactivateAndCloseClients(t, clients)

		ctx := context.Background()
		d1 := document.New(helper.TestDocKey(t))
		assert.NoError(t, c1.Attach(ctx, d1))
		d2 := document.New(helper.TestDocKey(t))
		assert.NoError(t, c2.Attach(ctx, d2))

		assert.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetNewTree("t", &initialState)
			return nil
		}))
		assert.NoError(t, c1.Sync(ctx))
		assert.NoError(t, c2.Sync(ctx))
		assert.Equal(t, initialXML, d1.Root().GetTree("t").ToXML())
		assert.Equal(t, initialXML, d2.Root().GetTree("t").ToXML())

		runEditOperation(t, d1, 0, ranges, op1)
		runStyleOperation(t, d2, 1, ranges, op2)

		return syncClientsThenCheckEqual(t, []clientAndDocPair{{c1, d1}, {c2, d2}})
	}

	for _, interval := range rangesToTestMixedTypeOperation {
		for _, op1 := range editOperationsToTest {
			for _, op2 := range styleOperationsToTest {
				desc := "concurrently edit-style test-" + interval.desc + "("
				desc += op1.desc + "," + op2.desc + ")"
				t.Run(desc, func(t *testing.T) {
					if !runEditStyleTest(interval, op1, op2) {
						t.Skip()
					}
				})
			}
		}
	}
}
