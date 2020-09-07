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
		return json.NewObject(json.NewRHTPriorityQueueMap(), time.InitialTicket), nil
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
	case *api.JSONElement_RichText_:
		return fromJSONRichText(decoded.RichText)
	case *api.JSONElement_Counter_:
		return fromJSONCounter(decoded.Counter)
	default:
		panic("unsupported element")
	}
}

func fromJSONObject(pbObj *api.JSONElement_Object) *json.Object {
	members := json.NewRHTPriorityQueueMap()
	for _, pbNode := range pbObj.Nodes {
		members.Set(pbNode.Key, fromJSONElement(pbNode.Element))
	}

	obj := json.NewObject(
		members,
		fromTimeTicket(pbObj.CreatedAt),
	)
	obj.SetUpdatedAt(fromTimeTicket(pbObj.UpdatedAt))
	obj.Remove(fromTimeTicket(pbObj.RemovedAt))
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
	arr.SetUpdatedAt(fromTimeTicket(pbArr.UpdatedAt))
	arr.Remove(fromTimeTicket(pbArr.RemovedAt))
	return arr
}

func fromJSONPrimitive(pbPrim *api.JSONElement_Primitive) *json.Primitive {
	primitive := json.NewPrimitive(
		json.ValueFromBytes(fromValueType(pbPrim.Type), pbPrim.Value),
		fromTimeTicket(pbPrim.CreatedAt),
	)
	primitive.SetUpdatedAt(fromTimeTicket(pbPrim.UpdatedAt))
	primitive.Remove(fromTimeTicket(pbPrim.RemovedAt))
	return primitive
}

func fromJSONText(pbText *api.JSONElement_Text) *json.Text {
	rgaTreeSplit := json.NewRGATreeSplit(json.InitialTextNode())

	current := rgaTreeSplit.InitialHead()
	for _, pbNode := range pbText.Nodes {
		textNode := fromTextNode(pbNode)
		current = rgaTreeSplit.InsertAfter(current, textNode)
		insPrevID := fromTextNodeID(pbNode.InsPrevId)
		if insPrevID != nil {
			insPrevNode := rgaTreeSplit.FindNode(insPrevID)
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
	text.SetUpdatedAt(fromTimeTicket(pbText.UpdatedAt))
	text.Remove(fromTimeTicket(pbText.RemovedAt))

	return text
}

func fromJSONRichText(pbText *api.JSONElement_RichText) *json.RichText {
	rgaTreeSplit := json.NewRGATreeSplit(json.InitialRichTextNode())

	current := rgaTreeSplit.InitialHead()
	for _, pbNode := range pbText.Nodes {
		textNode := fromRichTextNode(pbNode)
		current = rgaTreeSplit.InsertAfter(current, textNode)
		insPrevID := fromTextNodeID(pbNode.InsPrevId)
		if insPrevID != nil {
			insPrevNode := rgaTreeSplit.FindNode(insPrevID)
			if insPrevNode == nil {
				log.Logger.Warn("insPrevNode should be presence")
			}
			current.SetInsPrev(insPrevNode)
		}
	}

	text := json.NewRichText(
		rgaTreeSplit,
		fromTimeTicket(pbText.CreatedAt),
	)
	text.SetUpdatedAt(fromTimeTicket(pbText.UpdatedAt))
	text.Remove(fromTimeTicket(pbText.RemovedAt))

	return text
}

func fromJSONCounter(pbCnt *api.JSONElement_Counter) *json.Counter {
	counter := json.NewCounter(
		json.CounterValueFromBytes(fromCounterType(pbCnt.Type), pbCnt.Value),
		fromTimeTicket(pbCnt.CreatedAt),
	)
	counter.SetUpdatedAt(fromTimeTicket(pbCnt.UpdatedAt))
	counter.Remove(fromTimeTicket(pbCnt.RemovedAt))
	return counter
}

func fromTextNode(pbTextNode *api.TextNode) *json.RGATreeSplitNode {
	textNode := json.NewRGATreeSplitNode(
		fromTextNodeID(pbTextNode.Id),
		json.NewTextValue(pbTextNode.Value),
	)
	if pbTextNode.RemovedAt != nil {
		textNode.Remove(fromTimeTicket(pbTextNode.RemovedAt), time.MaxTicket)
	}
	return textNode
}

func fromRichTextNode(pbNode *api.RichTextNode) *json.RGATreeSplitNode {
	attrs := json.NewRHT()
	for _, pbAttr := range pbNode.Attributes {
		attrs.Set(pbAttr.Key, pbAttr.Value, fromTimeTicket(pbAttr.UpdatedAt))
	}

	textNode := json.NewRGATreeSplitNode(
		fromTextNodeID(pbNode.Id),
		json.NewRichTextValue(attrs, pbNode.Value),
	)
	if pbNode.RemovedAt != nil {
		textNode.Remove(fromTimeTicket(pbNode.RemovedAt), time.MaxTicket)
	}
	return textNode
}

func fromTextNodeID(pbTextNodeID *api.TextNodeID) *json.RGATreeSplitNodeID {
	if pbTextNodeID == nil {
		return nil
	}

	return json.NewRGATreeSplitNodeID(
		fromTimeTicket(pbTextNodeID.CreatedAt),
		int(pbTextNodeID.Offset),
	)
}
