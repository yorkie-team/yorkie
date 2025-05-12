/*
 * Copyright 2025 The Yorkie Authors. All rights reserved.
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

package yson

import (
	"fmt"
	"reflect"

	"github.com/yorkie-team/yorkie/pkg/document/crdt"
)

// FromCRDT converts a CRDT element to a YSON element.
func FromCRDT(elem crdt.Element) (interface{}, error) {
	switch elem := elem.(type) {
	case *crdt.Object:
		return toObject(elem)
	case *crdt.Array:
		return toArray(elem)
	case *crdt.Primitive:
		return toPrimitive(elem), nil
	case *crdt.Counter:
		return toCounter(elem)
	case *crdt.Text:
		return toText(elem), nil
	case *crdt.Tree:
		return toTree(elem), nil
	default:
		return nil, fmt.Errorf("%v: %w", reflect.TypeOf(elem), ErrUnsupported)
	}
}

func toObject(object *crdt.Object) (Object, error) {
	fields := make(Object)
	for key, elem := range object.Members() {
		elemStruct, err := FromCRDT(elem)
		if err != nil {
			return nil, err
		}
		fields[key] = elemStruct
	}
	return fields, nil
}

func toArray(array *crdt.Array) (Array, error) {
	var elements Array
	for _, elem := range array.Elements() {
		pbElem, err := FromCRDT(elem)
		if err != nil {
			return nil, err
		}
		elements = append(elements, pbElem)
	}
	return elements, nil
}

func toPrimitive(primitive *crdt.Primitive) interface{} {
	return primitive.Value()
}

func toCounter(counter *crdt.Counter) (Counter, error) {
	return Counter{
		Type:  counter.ValueType(),
		Value: counter.Value(),
	}, nil
}

func toText(text *crdt.Text) Text {
	nodes := make([]TextNode, 0)
	for _, node := range text.Nodes() {
		if node.RemovedAt() != nil {
			continue
		}
		value := node.Value()

		var attrs map[string]string
		if value.Attrs() != nil && value.Attrs().Len() > 0 {
			attrs = value.Attrs().Elements()
		}

		nodes = append(nodes, TextNode{
			Value:      value.Value(),
			Attributes: attrs,
		})
	}
	return Text{Nodes: nodes}
}

func toTree(crdtTree *crdt.Tree) Tree {
	var buildTreeNode func(crdtNode *crdt.TreeNode) TreeNode
	buildTreeNode = func(crdtNode *crdt.TreeNode) TreeNode {
		var attrs map[string]string
		if crdtNode.Attrs != nil && crdtNode.Attrs.Len() > 0 {
			attrs = crdtNode.Attrs.Elements()
		}

		node := TreeNode{
			Type:       crdtNode.Type(),
			Attributes: attrs,
			Value:      crdtNode.Value,
		}

		for _, child := range crdtNode.Children() {
			node.Children = append(node.Children, buildTreeNode(child))
		}

		return node
	}

	// Start building from the root node
	root := buildTreeNode(crdtTree.Root())

	return Tree{
		Root: root,
	}
}
