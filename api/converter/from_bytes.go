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
	"github.com/gogo/protobuf/proto"

	"github.com/yorkie-team/yorkie/api"
	"github.com/yorkie-team/yorkie/pkg/document/json"
	"github.com/yorkie-team/yorkie/pkg/document/time"
	"github.com/yorkie-team/yorkie/pkg/log"
)

func BytesToObject(snapshot []byte) (*json.Object, error) {
	if snapshot == nil {
		return json.NewObject(json.NewRHT(), time.InitialTicket), nil
	}

	pbElem := &api.JSONElement{}
	if err := proto.Unmarshal(snapshot, pbElem); err != nil {
		return nil, err
	}

	return fromJSONObject(pbElem.GetObject()), nil
}

func fromJSONElement(pbElem *api.JSONElement) json.Element {
	switch decoded := pbElem.Body.(type) {
	case *api.JSONElement_Object_:
		return fromJSONObject(decoded.Object)
	case *api.JSONElement_Array_:
		return fromJSONArray(decoded.Array)
	case *api.JSONElement_Primitive_:
		return fromJSONPrimitive(decoded.Primitive)
	case *api.JSONElement_Text_:
		return fromJSONText(decoded.Text)
	default:
		panic("unsupported element")
	}
}

func fromJSONObject(pbObj *api.JSONElement_Object) *json.Object {
	members := json.NewRHT()
	for _, pbNode := range pbObj.Nodes {
		members.Set(pbNode.Key, fromJSONElement(pbNode.Element))
	}

	obj := json.NewObject(
		members,
		fromTimeTicket(pbObj.CreatedAt),
	)
	obj.Delete(fromTimeTicket(pbObj.DeletedAt))
	return obj
}

func fromJSONArray(pbArr *api.JSONElement_Array) *json.Array {
	elements := json.NewRGATreeList()
	for _, pbNode := range pbArr.Nodes {
		elements.Add(fromJSONElement(pbNode.Element))
	}

	arr := json.NewArray(
		elements,
		fromTimeTicket(pbArr.CreatedAt),
	)
	arr.Delete(fromTimeTicket(pbArr.DeletedAt))
	return arr
}

func fromJSONPrimitive(pbPrim *api.JSONElement_Primitive) *json.Primitive {
	primitive := json.NewPrimitive(
		json.ValueFromBytes(fromValueType(pbPrim.Type), pbPrim.Value),
		fromTimeTicket(pbPrim.CreatedAt),
	)
	primitive.Delete(fromTimeTicket(pbPrim.DeletedAt))
	return primitive
}

func fromJSONText(pbText *api.JSONElement_Text) *json.Text {
	rgaTreeSplit := json.NewRGATreeSplit()

	current := rgaTreeSplit.InitialHead()
	for _, pbNode := range pbText.Nodes {
		textNode := fromTextNode(pbNode)
		current = rgaTreeSplit.InsertAfter(current, textNode)
		insPrevID := fromTextNodeID(pbNode.InsPrevId)
		if insPrevID != nil {
			insPrevNode := rgaTreeSplit.FindTextNode(insPrevID)
			if insPrevNode == nil {
				log.Logger.Warn("insPrevNode should be presence")
			}
			current.SetInsPrev(insPrevNode)
		}
	}

	text := json.NewText(
		rgaTreeSplit,
		fromTimeTicket(pbText.CreatedAt),
	)
	text.Delete(fromTimeTicket(pbText.DeletedAt))

	return text
}

func fromTextNode(pbTextNode *api.TextNode) *json.TextNode {
	textNode := json.NewTextNode(
		fromTextNodeID(pbTextNode.Id),
		pbTextNode.Value,
	)
	if pbTextNode.DeletedAt != nil {
		textNode.Delete(fromTimeTicket(pbTextNode.DeletedAt), time.MaxTicket)
	}
	return textNode
}

func fromTextNodeID(pbTextNodeID *api.TextNodeID) *json.TextNodeID {
	if pbTextNodeID == nil {
		return nil
	}

	return json.NewTextNodeID(
		fromTimeTicket(pbTextNodeID.CreatedAt),
		int(pbTextNodeID.Offset),
	)
}
