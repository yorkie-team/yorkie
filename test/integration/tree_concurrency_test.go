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

func parseSimpleXML(s string) []string {
	res := []string{}
	for i := 0; i < len(s); i++ {
		now := ``
		if s[i] == '<' {
			for i < len(s) && s[i] != '>' {
				now += string(s[i])
				i++
			}
			now += string(s[i])
		} else {
			now += string(s[i])
		}
		res = append(res, now)
	}
	return res
}

type testResult struct {
	flag       bool
	resultDesc string
}

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

func getMergeRange(xml string, interval rangeType) rangeType {
	content := parseSimpleXML(xml)
	st, ed := -1, -1
	for i := interval.from + 1; i <= interval.to; i++ {
		if st == -1 && len(content[i]) >= 2 && content[i][0] == '<' && content[i][1] == '/' {
			st = i - 1
		}
		if len(content[i]) >= 2 && content[i][0] == '<' && content[i][1] != '/' {
			ed = i
		}
	}
	return rangeType{st, ed}
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
	MergeUpdate
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
		if op.op == EditUpdate {
			root.GetTree("t").Edit(from, to, op.content, op.splitLevel)
		} else if op.op == MergeUpdate {
			mergeInterval := getMergeRange(root.GetTree("t").ToXML(), interval)
			from, to = mergeInterval.from, mergeInterval.to
			if from != -1 && to != -1 && from < to {
				root.GetTree("t").Edit(mergeInterval.from, mergeInterval.to, op.content, op.splitLevel)
			}
		}
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

	runTest := func(ranges twoRangesType, op1, op2 operationInterface) testResult {
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

		flag := syncClientsThenCheckEqual(t, []clientAndDocPair{{c1, d1}, {c2, d2}})
		if flag {
			return testResult{flag, `pass`}
		}
		return testResult{flag, `different result`}
	}

	for _, interval := range rangesArr {
		for _, op1 := range opArr1 {
			for _, op2 := range opArr2 {
				desc := testDesc + "-" + interval.desc
				desc += "(" + op1.getDesc() + "," + op2.getDesc() + ")"
				t.Run(desc, func(t *testing.T) {
					result := runTest(interval, op1, op2)
					if !result.flag {
						t.Skip(result.resultDesc)
					}
				})
			}
		}
	}
}

func TestTreeConcurrencyEditEdit(t *testing.T) {
	//       0   1 2 3 4    5   6 7 8 9    10   11 12 13 14    15
	// <root> <p> a b c </p> <p> d e f </p>  <p>  g  h  i  </p>  </root>

	initialState := json.TreeNode{
		Type: "root",
		Children: []json.TreeNode{
			{Type: "p", Children: []json.TreeNode{{Type: "text", Value: "abc"}}},
			{Type: "p", Children: []json.TreeNode{{Type: "text", Value: "def"}}},
			{Type: "p", Children: []json.TreeNode{{Type: "text", Value: "ghi"}}},
		},
	}
	initialXML := `<root><p>abc</p><p>def</p><p>ghi</p></root>`

	textNode1 := &json.TreeNode{Type: "text", Value: "A"}
	textNode2 := &json.TreeNode{Type: "text", Value: "B"}
	elementNode1 := &json.TreeNode{Type: "b", Children: []json.TreeNode{}}
	elementNode2 := &json.TreeNode{Type: "i", Children: []json.TreeNode{}}

	ranges := []twoRangesType{
		// intersect-element: <p>abc</p><p>def</p> - <p>def</p><p>ghi</p>
		makeTwoRanges(0, 5, 10, 5, 10, 15, `intersect-element`),
		// intersect-text: ab - bc
		makeTwoRanges(1, 2, 3, 2, 3, 4, `intersect-text`),
		// contain-element: <p>abc</p><p>def</p><p>ghi</p> - <p>def</p>
		makeTwoRanges(0, 5, 15, 5, 5, 10, `contain-element`),
		// contain-text: abc - b
		makeTwoRanges(1, 2, 4, 2, 2, 3, `contain-text`),
		// contain-mixed-type: <p>abc</p><p>def</p><p>ghi</p> - def
		makeTwoRanges(0, 5, 15, 6, 7, 9, `contain-mixed-type`),
		// side-by-side-element: <p>abc</p> - <p>def</p>
		makeTwoRanges(0, 5, 5, 5, 5, 10, `side-by-side-element`),
		// side-by-side-text: a - bc
		makeTwoRanges(1, 1, 2, 2, 3, 4, `side-by-side-text`),
		// equal-element: <p>abc</p><p>def</p> - <p>abc</p><p>def</p>
		makeTwoRanges(0, 5, 10, 0, 5, 10, `equal-element`),
		// equal-text: abc - abc
		makeTwoRanges(1, 2, 4, 1, 2, 4, `equal-text`),
	}

	editOperations1 := []operationInterface{
		editOperationType{RangeFront, EditUpdate, textNode1, 0, `insertTextFront`},
		editOperationType{RangeMiddle, EditUpdate, textNode1, 0, `insertTextMiddle`},
		editOperationType{RangeBack, EditUpdate, textNode1, 0, `insertTextBack`},
		editOperationType{RangeAll, EditUpdate, textNode1, 0, `replaceText`},
		editOperationType{RangeFront, EditUpdate, elementNode1, 0, `insertElementFront`},
		editOperationType{RangeMiddle, EditUpdate, elementNode1, 0, `insertElementMiddle`},
		editOperationType{RangeBack, EditUpdate, elementNode1, 0, `insertElementBack`},
		editOperationType{RangeAll, EditUpdate, elementNode1, 0, `replaceElement`},
		editOperationType{RangeAll, EditUpdate, nil, 0, `delete`},
		editOperationType{RangeAll, MergeUpdate, nil, 0, `merge`},
	}

	editOperations2 := []operationInterface{
		editOperationType{RangeFront, EditUpdate, textNode2, 0, `insertTextFront`},
		editOperationType{RangeMiddle, EditUpdate, textNode2, 0, `insertTextMiddle`},
		editOperationType{RangeBack, EditUpdate, textNode2, 0, `insertTextBack`},
		editOperationType{RangeAll, EditUpdate, textNode2, 0, `replaceText`},
		editOperationType{RangeFront, EditUpdate, elementNode2, 0, `insertElementFront`},
		editOperationType{RangeMiddle, EditUpdate, elementNode2, 0, `insertElementMiddle`},
		editOperationType{RangeBack, EditUpdate, elementNode2, 0, `insertElementBack`},
		editOperationType{RangeAll, EditUpdate, elementNode2, 0, `replaceElement`},
		editOperationType{RangeAll, EditUpdate, nil, 0, `delete`},
		editOperationType{RangeAll, MergeUpdate, nil, 0, `merge`},
	}

	RunTestTreeConcurrency("concurrently-edit-edit-test", t, initialState, initialXML, ranges, editOperations1, editOperations2)
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
		// equal: <p>b</p> - <p>b</p>
		makeTwoRanges(3, -1, 6, 3, -1, 6, `equal`),
		// contain: <p>a</p><p>b</p><p>c</p> - <p>b</p>
		makeTwoRanges(0, -1, 9, 3, -1, 6, `contain`),
		// intersect: <p>a</p><p>b</p> - <p>b</p><p>c</p>
		makeTwoRanges(0, -1, 6, 3, -1, 9, `intersect`),
		// side-by-side: <p>a</p> - <p>b</p>
		makeTwoRanges(0, -1, 3, 3, -1, 6, `side-by-side`),
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
		// equal: <p>b</p> - <p>b</p>
		makeTwoRanges(3, 3, 6, 3, -1, 6, `equal`),
		// equal multiple: <p>a</p><p>b</p><p>c</p> - <p>a</p><p>b</p><p>c</p>
		makeTwoRanges(0, 3, 9, 0, 3, 9, `equal multiple`),
		// A contains B: <p>a</p><p>b</p><p>c</p> - <p>b</p>
		makeTwoRanges(0, 3, 9, 3, -1, 6, `A contains B`),
		// B contains A: <p>b</p> - <p>a</p><p>b</p><p>c</p>
		makeTwoRanges(3, 3, 6, 0, -1, 9, `B contains A`),
		// intersect: <p>a</p><p>b</p> - <p>b</p><p>c</p>
		makeTwoRanges(0, 3, 6, 3, -1, 9, `intersect`),
		// A -> B: <p>a</p> - <p>b</p>
		makeTwoRanges(0, 3, 3, 3, -1, 6, `A -> B`),
		// B -> A: <p>b</p> - <p>a</p>
		makeTwoRanges(3, 3, 6, 0, -1, 3, `B -> A`),
	}

	editOperations := []operationInterface{
		editOperationType{RangeFront, EditUpdate, content, 0, `insertFront`},
		editOperationType{RangeMiddle, EditUpdate, content, 0, `insertMiddle`},
		editOperationType{RangeBack, EditUpdate, content, 0, `insertBack`},
		editOperationType{RangeAll, EditUpdate, nil, 0, `delete`},
		editOperationType{RangeAll, EditUpdate, content, 0, `replace`},
		editOperationType{RangeAll, MergeUpdate, nil, 0, `merge`},
	}

	styleOperations := []operationInterface{
		styleOperationType{RangeAll, StyleRemove, "bold", "", `remove-bold`},
		styleOperationType{RangeAll, StyleSet, "bold", "aa", `set-bold-aa`},
	}

	RunTestTreeConcurrency("concurrently-edit-style-test", t, initialState, initialXML, ranges, editOperations, styleOperations)
}
