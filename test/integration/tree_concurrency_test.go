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

type rangeSelector int

const (
	RangeUnknown rangeSelector = iota
	RangeFront
	RangeMiddle
	RangeBack
	RangeAll
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

func makeTwoRanges(from1, mid1, to1 int, from2, mid2, to2 int, desc string) twoRangesType {
	range0 := rangeWithMiddleType{from1, mid1, to1}
	range1 := rangeWithMiddleType{from2, mid2, to2}
	return twoRangesType{[2]rangeWithMiddleType{range0, range1}, desc}
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

type operationInterface interface {
	run(t *testing.T, doc *document.Document, user int, ranges twoRangesType)
	getDesc() string
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

func (op styleOperationType) getDesc() string {
	return op.desc
}

func (op editOperationType) getDesc() string {
	return op.desc
}

func (op styleOperationType) run(t *testing.T, doc *document.Document, user int, ranges twoRangesType) {
	interval := getRange(ranges, op.selector, user)
	from, to := interval.from, interval.to

	assert.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
		if op.op == StyleRemove {
			root.GetTree("t").RemoveStyle(from, to, []string{op.key})
		} else if op.op == StyleSet {
			root.GetTree("t").Style(from, to, map[string]string{op.key: op.value})
		}
		return nil
	}))
}

func (op editOperationType) run(t *testing.T, doc *document.Document, user int, ranges twoRangesType) {
	interval := getRange(ranges, op.selector, user)
	from, to := interval.from, interval.to

	assert.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
		root.GetTree("t").Edit(from, to, op.content, op.splitLevel)
		return nil
	}))
}

// testDesc: description of test set
// initialState, initialXML: initial state of document
// rangeArr: ranges to perform operation
// opArr1: operations to perform by first user
// opArr2: operations to perform by second user
func RunTestTreeConcurrency(testDesc string, t *testing.T, initialState json.TreeNode, initialXML string,
	rangesArr []twoRangesType, opArr1, opArr2 []operationInterface) {

	runTest := func(ranges twoRangesType, op1, op2 operationInterface) bool {
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

		op1.run(t, d1, 0, ranges)
		op2.run(t, d2, 1, ranges)

		return syncClientsThenCheckEqual(t, []clientAndDocPair{{c1, d1}, {c2, d2}})
	}

	for _, interval := range rangesArr {
		for _, op1 := range opArr1 {
			for _, op2 := range opArr2 {
				desc := testDesc + "-" + interval.desc
				desc += "(" + op1.getDesc() + "," + op2.getDesc() + ")"
				t.Run(desc, func(t *testing.T) {
					if !runTest(interval, op1, op2) {
						t.Skip()
					}
				})
			}
		}
	}
}

func TestTreeConcurrencyStyleStyle(t *testing.T) {
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

	ranges := []twoRangesType{
		makeTwoRanges(3, -1, 6, 3, -1, 6, `equal`),        // (3, 6) - (3, 6)
		makeTwoRanges(0, -1, 9, 3, -1, 6, `contain`),      // (0, 9) - (3, 6)
		makeTwoRanges(0, -1, 6, 3, -1, 9, `intersect`),    // (0, 6) - (3, 9)
		makeTwoRanges(0, -1, 3, 3, -1, 6, `side-by-side`), // (0, 3) - (3, 6)
	}

	styleOperations := []operationInterface{
		styleOperationType{RangeAll, StyleRemove, "bold", "", `remove-bold`},
		styleOperationType{RangeAll, StyleSet, "bold", "aa", `set-bold-aa`},
		styleOperationType{RangeAll, StyleSet, "bold", "bb", `set-bold-bb`},
		styleOperationType{RangeAll, StyleRemove, "italic", "", `remove-italic`},
		styleOperationType{RangeAll, StyleSet, "italic", "aa", `set-italic-aa`},
		styleOperationType{RangeAll, StyleSet, "italic", "bb", `set-italic-bb`},
	}

	RunTestTreeConcurrency("concurrently-style-style-test", t, initialState, initialXML, ranges, styleOperations, styleOperations)
}

func TestTreeConcurrencyEditStyle(t *testing.T) {
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

	ranges := []twoRangesType{
		makeTwoRanges(3, 3, 6, 3, -1, 6, `equal`),        // (3, 6) - (3, 6)
		makeTwoRanges(0, 3, 9, 3, -1, 6, `A contains B`), // (0, 9) - (3, 6)
		makeTwoRanges(3, 3, 6, 0, -1, 9, `B contains A`), // (0, 9) - (3, 6)
		makeTwoRanges(0, 3, 6, 3, -1, 9, `intersect`),    // (0, 6) - (3, 9)
		makeTwoRanges(0, 3, 3, 3, -1, 6, `A -> B`),       // (0, 3) - (3, 6)
		makeTwoRanges(3, 3, 6, 0, -1, 3, `B -> A`),       // (0, 3) - (3, 6)
	}

	editOperations := []operationInterface{
		editOperationType{RangeFront, EditUpdate, content, 0, `insertFront`},
		editOperationType{RangeMiddle, EditUpdate, content, 0, `insertMiddle`},
		editOperationType{RangeBack, EditUpdate, content, 0, `insertBack`},
		editOperationType{RangeAll, EditUpdate, nil, 0, `erase`},
		editOperationType{RangeAll, EditUpdate, content, 0, `change`},
	}

	styleOperations := []operationInterface{
		styleOperationType{RangeAll, StyleRemove, "bold", "", `remove-bold`},
		styleOperationType{RangeAll, StyleSet, "bold", "aa", `set-bold-aa`},
	}

	RunTestTreeConcurrency("concurrently-edit-style-test", t, initialState, initialXML, ranges, editOperations, styleOperations)
}
