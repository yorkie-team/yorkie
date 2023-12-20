/*
 * Copyright 2020 The Yorkie Authors. All rights reserved.
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

package converter

import (
	"errors"
	"fmt"

	"google.golang.org/protobuf/proto"

	api "github.com/yorkie-team/yorkie/api/yorkie/v1"
	"github.com/yorkie-team/yorkie/pkg/document/crdt"
	"github.com/yorkie-team/yorkie/pkg/document/innerpresence"
	"github.com/yorkie-team/yorkie/pkg/document/time"
)

// BytesToSnapshot creates a Snapshot from the given byte array.
func BytesToSnapshot(snapshot []byte) (*crdt.Object, *innerpresence.Map, error) {
	if len(snapshot) == 0 {
		return crdt.NewObject(crdt.NewElementRHT(), time.InitialTicket), innerpresence.NewMap(), nil
	}

	pbSnapshot := &api.Snapshot{}
	if err := proto.Unmarshal(snapshot, pbSnapshot); err != nil {
		return nil, nil, fmt.Errorf("unmarshal snapshot: %w", err)
	}

	obj, err := fromJSONElement(pbSnapshot.GetRoot())
	if err != nil {
		return nil, nil, err
	}

	presences := fromPresences(pbSnapshot.GetPresences())
	return obj.(*crdt.Object), presences, nil
}

// BytesToObject creates an Object from the given byte array.
func BytesToObject(snapshot []byte) (*crdt.Object, error) {
	if len(snapshot) == 0 {
		return nil, errors.New("snapshot should not be empty")
	}

	pbElem := &api.JSONElement{}
	if err := proto.Unmarshal(snapshot, pbElem); err != nil {
		return nil, fmt.Errorf("unmarshal element: %w", err)
	}

	obj, err := fromJSONObject(pbElem.GetJsonObject())
	if err != nil {
		return nil, err
	}

	return obj, nil
}

// BytesToArray creates a Array from the given byte array.
func BytesToArray(snapshot []byte) (*crdt.Array, error) {
	if len(snapshot) == 0 {
		return nil, errors.New("snapshot should not be empty")
	}

	pbArray := &api.JSONElement{}
	if err := proto.Unmarshal(snapshot, pbArray); err != nil {
		return nil, fmt.Errorf("unmarshal array: %w", err)
	}

	array, err := fromJSONArray(pbArray.GetJsonArray())
	if err != nil {
		return nil, err
	}

	return array, nil
}

// BytesToTree creates a Tree from the given byte array.
func BytesToTree(snapshot []byte) (*crdt.Tree, error) {
	if len(snapshot) == 0 {
		return nil, errors.New("snapshot should not be empty")
	}

	pbTree := &api.JSONElement{}
	if err := proto.Unmarshal(snapshot, pbTree); err != nil {
		return nil, fmt.Errorf("unmarshal tree: %w", err)
	}

	tree, err := fromJSONTree(pbTree.GetTree())
	if err != nil {
		return nil, err
	}

	return tree, nil
}

func fromJSONElement(pbElem *api.JSONElement) (crdt.Element, error) {
	switch decoded := pbElem.Body.(type) {
	case *api.JSONElement_JsonObject:
		return fromJSONObject(decoded.JsonObject)
	case *api.JSONElement_JsonArray:
		return fromJSONArray(decoded.JsonArray)
	case *api.JSONElement_Primitive_:
		return fromJSONPrimitive(decoded.Primitive)
	case *api.JSONElement_Text_:
		return fromJSONText(decoded.Text)
	case *api.JSONElement_Counter_:
		return fromJSONCounter(decoded.Counter)
	case *api.JSONElement_Tree_:
		return fromJSONTree(decoded.Tree)
	default:
		return nil, fmt.Errorf("%s: %w", decoded, ErrUnsupportedElement)
	}
}

func fromJSONObject(pbObj *api.JSONElement_JSONObject) (*crdt.Object, error) {
	members := crdt.NewElementRHT()
	for _, pbNode := range pbObj.Nodes {
		elem, err := fromJSONElement(pbNode.Element)
		if err != nil {
			return nil, err
		}
		members.Set(pbNode.Key, elem)
	}

	createdAt, err := fromTimeTicket(pbObj.CreatedAt)
	if err != nil {
		return nil, err
	}

	movedAt, err := fromTimeTicket(pbObj.MovedAt)
	if err != nil {
		return nil, err
	}

	removedAt, err := fromTimeTicket(pbObj.RemovedAt)
	if err != nil {
		return nil, err
	}

	obj := crdt.NewObject(
		members,
		createdAt,
	)
	obj.SetMovedAt(movedAt)
	obj.SetRemovedAt(removedAt)

	return obj, nil
}

func fromJSONArray(pbArr *api.JSONElement_JSONArray) (*crdt.Array, error) {
	elements := crdt.NewRGATreeList()
	for _, pbNode := range pbArr.Nodes {
		elem, err := fromJSONElement(pbNode.Element)
		if err != nil {
			return nil, err
		}
		if err = elements.Add(elem); err != nil {
			return nil, err
		}
	}

	createdAt, err := fromTimeTicket(pbArr.CreatedAt)
	if err != nil {
		return nil, err
	}
	movedAt, err := fromTimeTicket(pbArr.MovedAt)
	if err != nil {
		return nil, err
	}
	removedAt, err := fromTimeTicket(pbArr.RemovedAt)
	if err != nil {
		return nil, err
	}

	arr := crdt.NewArray(
		elements,
		createdAt,
	)
	arr.SetMovedAt(movedAt)
	arr.SetRemovedAt(removedAt)
	return arr, nil
}

func fromJSONPrimitive(
	pbPrim *api.JSONElement_Primitive,
) (*crdt.Primitive, error) {
	createdAt, err := fromTimeTicket(pbPrim.CreatedAt)
	if err != nil {
		return nil, err
	}
	movedAt, err := fromTimeTicket(pbPrim.MovedAt)
	if err != nil {
		return nil, err
	}
	removedAt, err := fromTimeTicket(pbPrim.RemovedAt)
	if err != nil {
		return nil, err
	}
	valueType, err := fromPrimitiveValueType(pbPrim.Type)
	if err != nil {
		return nil, err
	}
	value, err := crdt.ValueFromBytes(valueType, pbPrim.Value)
	if err != nil {
		return nil, err
	}
	primitive, err := crdt.NewPrimitive(value, createdAt)
	if err != nil {
		return nil, err
	}
	primitive.SetMovedAt(movedAt)
	primitive.SetRemovedAt(removedAt)
	return primitive, nil
}

func fromJSONText(
	pbText *api.JSONElement_Text,
) (*crdt.Text, error) {
	createdAt, err := fromTimeTicket(pbText.CreatedAt)
	if err != nil {
		return nil, err
	}
	movedAt, err := fromTimeTicket(pbText.MovedAt)
	if err != nil {
		return nil, err
	}
	removedAt, err := fromTimeTicket(pbText.RemovedAt)
	if err != nil {
		return nil, err
	}

	rgaTreeSplit := crdt.NewRGATreeSplit(crdt.InitialTextNode())
	current := rgaTreeSplit.InitialHead()
	for _, pbNode := range pbText.Nodes {
		textNode, err := fromTextNode(pbNode)
		if err != nil {
			return nil, err
		}
		current = rgaTreeSplit.InsertAfter(current, textNode)
		insPrevID, err := fromTextNodeID(pbNode.InsPrevId)
		if err != nil {
			return nil, err
		}
		if insPrevID != nil {
			insPrevNode := rgaTreeSplit.FindNode(insPrevID)
			if insPrevNode == nil {
				return nil, fmt.Errorf("insPrevNode should be presence")
			}
			current.SetInsPrev(insPrevNode)
		}
	}

	text := crdt.NewText(
		rgaTreeSplit,
		createdAt,
	)
	text.SetMovedAt(movedAt)
	text.SetRemovedAt(removedAt)

	return text, nil
}

func fromJSONCounter(pbCnt *api.JSONElement_Counter) (*crdt.Counter, error) {
	createdAt, err := fromTimeTicket(pbCnt.CreatedAt)
	if err != nil {
		return nil, err
	}
	movedAt, err := fromTimeTicket(pbCnt.MovedAt)
	if err != nil {
		return nil, err
	}
	removedAt, err := fromTimeTicket(pbCnt.RemovedAt)
	if err != nil {
		return nil, err
	}
	counterType, err := fromCounterType(pbCnt.Type)
	if err != nil {
		return nil, err
	}
	counterValue, err := crdt.CounterValueFromBytes(counterType, pbCnt.Value)
	if err != nil {
		return nil, err
	}

	counter, err := crdt.NewCounter(
		counterType,
		counterValue,
		createdAt,
	)
	if err != nil {
		return nil, err
	}
	counter.SetMovedAt(movedAt)
	counter.SetRemovedAt(removedAt)

	return counter, nil
}

func fromTextNode(
	pbNode *api.TextNode,
) (*crdt.RGATreeSplitNode[*crdt.TextValue], error) {
	id, err := fromTextNodeID(pbNode.Id)
	if err != nil {
		return nil, err
	}

	attrs := crdt.NewRHT()
	for key, pbAttr := range pbNode.Attributes {
		updatedAt, err := fromTimeTicket(pbAttr.UpdatedAt)
		if err != nil {
			return nil, err
		}
		attrs.Set(key, pbAttr.Value, updatedAt)
	}

	textNode := crdt.NewRGATreeSplitNode(
		id,
		crdt.NewTextValue(pbNode.Value, attrs),
	)
	if pbNode.RemovedAt != nil {
		removedAt, err := fromTimeTicket(pbNode.RemovedAt)
		if err != nil {
			return nil, err
		}
		textNode.Remove(removedAt, time.MaxTicket)
	}
	return textNode, nil
}

func fromTextNodeID(
	pbTextNodeID *api.TextNodeID,
) (*crdt.RGATreeSplitNodeID, error) {
	if pbTextNodeID == nil {
		return nil, nil
	}

	createdAt, err := fromTimeTicket(pbTextNodeID.CreatedAt)
	if err != nil {
		return nil, err
	}

	return crdt.NewRGATreeSplitNodeID(
		createdAt,
		int(pbTextNodeID.Offset),
	), nil
}

func fromJSONTree(
	pbTree *api.JSONElement_Tree,
) (*crdt.Tree, error) {
	createdAt, err := fromTimeTicket(pbTree.CreatedAt)
	if err != nil {
		return nil, err
	}
	movedAt, err := fromTimeTicket(pbTree.MovedAt)
	if err != nil {
		return nil, err
	}
	removedAt, err := fromTimeTicket(pbTree.RemovedAt)
	if err != nil {
		return nil, err
	}
	root, err := FromTreeNodes(pbTree.Nodes)
	if err != nil {
		return nil, err
	}

	tree := crdt.NewTree(
		root,
		createdAt,
	)
	tree.SetMovedAt(movedAt)
	tree.SetRemovedAt(removedAt)

	return tree, nil
}
