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
	"github.com/yorkie-team/yorkie/pkg/log"
)

// ObjectToBytes converts the given object to byte array.
func ObjectToBytes(obj *json.Object) ([]byte, error) {
	bytes, err := proto.Marshal(toJSONElement(obj))
	if err != nil {
		log.Logger.Error(err)
		return nil, err
	}
	return bytes, nil
}

func toJSONElement(elem json.Element) *api.JSONElement {
	switch elem := elem.(type) {
	case *json.Object:
		return toJSONObject(elem)
	case *json.Array:
		return toJSONArray(elem)
	case *json.Primitive:
		return toPrimitive(elem)
	case *json.Text:
		return toText(elem)
	case *json.RichText:
		return toRichText(elem)
	case *json.Counter:
		return toCounter(elem)
	}

	panic("fail to encode JSONElement to protobuf")
}

func toJSONObject(obj *json.Object) *api.JSONElement {
	pbElem := &api.JSONElement{
		Body: &api.JSONElement_Object_{Object: &api.JSONElement_Object{
			Nodes:     toRHTNodes(obj.RHTNodes()),
			CreatedAt: toTimeTicket(obj.CreatedAt()),
			UpdatedAt: toTimeTicket(obj.UpdatedAt()),
			RemovedAt: toTimeTicket(obj.RemovedAt()),
		}},
	}
	return pbElem
}

func toJSONArray(arr *json.Array) *api.JSONElement {
	pbElem := &api.JSONElement{
		Body: &api.JSONElement_Array_{Array: &api.JSONElement_Array{
			Nodes:     toRGANodes(arr.RGANodes()),
			CreatedAt: toTimeTicket(arr.CreatedAt()),
			UpdatedAt: toTimeTicket(arr.UpdatedAt()),
			RemovedAt: toTimeTicket(arr.RemovedAt()),
		}},
	}
	return pbElem
}

func toPrimitive(primitive *json.Primitive) *api.JSONElement {
	return &api.JSONElement{
		Body: &api.JSONElement_Primitive_{Primitive: &api.JSONElement_Primitive{
			Type:      toValueType(primitive.ValueType()),
			Value:     primitive.Bytes(),
			CreatedAt: toTimeTicket(primitive.CreatedAt()),
			UpdatedAt: toTimeTicket(primitive.UpdatedAt()),
			RemovedAt: toTimeTicket(primitive.RemovedAt()),
		}},
	}
}

func toText(text *json.Text) *api.JSONElement {
	return &api.JSONElement{
		Body: &api.JSONElement_Text_{Text: &api.JSONElement_Text{
			Nodes:     toTextNodes(text.Nodes()),
			CreatedAt: toTimeTicket(text.CreatedAt()),
			UpdatedAt: toTimeTicket(text.UpdatedAt()),
			RemovedAt: toTimeTicket(text.RemovedAt()),
		}},
	}
}

func toRichText(text *json.RichText) *api.JSONElement {
	return &api.JSONElement{
		Body: &api.JSONElement_RichText_{RichText: &api.JSONElement_RichText{
			Nodes:     toRichTextNodes(text.Nodes()),
			CreatedAt: toTimeTicket(text.CreatedAt()),
			UpdatedAt: toTimeTicket(text.UpdatedAt()),
			RemovedAt: toTimeTicket(text.RemovedAt()),
		}},
	}
}

func toCounter(counter *json.Counter) *api.JSONElement {
	return &api.JSONElement{
		Body: &api.JSONElement_Counter_{Counter: &api.JSONElement_Counter{
			Type:      toCounterType(counter.ValueType()),
			Value:     counter.Bytes(),
			CreatedAt: toTimeTicket(counter.CreatedAt()),
			UpdatedAt: toTimeTicket(counter.UpdatedAt()),
			RemovedAt: toTimeTicket(counter.RemovedAt()),
		}},
	}
}

func toRHTNodes(rhtNodes []*json.RHTPQMapNode) []*api.RHTNode {
	var pbRHTNodes []*api.RHTNode
	for _, rhtNode := range rhtNodes {
		pbRHTNodes = append(pbRHTNodes, &api.RHTNode{
			Key:     rhtNode.Key(),
			Element: toJSONElement(rhtNode.Element()),
		})
	}
	return pbRHTNodes
}

func toRGANodes(rgaNodes []*json.RGATreeListNode) []*api.RGANode {
	var pbRGANodes []*api.RGANode
	for _, rgaNode := range rgaNodes {
		pbRGANodes = append(pbRGANodes, &api.RGANode{
			Element: toJSONElement(rgaNode.Element()),
		})
	}
	return pbRGANodes
}

func toTextNodes(textNodes []*json.RGATreeSplitNode) []*api.TextNode {
	var pbTextNodes []*api.TextNode
	for _, textNode := range textNodes {
		pbTextNode := &api.TextNode{
			Id:        toTextNodeID(textNode.ID()),
			Value:     textNode.String(),
			RemovedAt: toTimeTicket(textNode.RemovedAt()),
		}

		if textNode.InsPrevID() != nil {
			pbTextNode.InsPrevId = toTextNodeID(textNode.InsPrevID())
		}

		pbTextNodes = append(pbTextNodes, pbTextNode)
	}
	return pbTextNodes
}

func toRichTextNodes(textNodes []*json.RGATreeSplitNode) []*api.RichTextNode {
	var pbTextNodes []*api.RichTextNode
	for _, textNode := range textNodes {
		value := textNode.Value().(*json.RichTextValue)

		attrs := make(map[string]*api.RichTextNodeAttr)
		for _, node := range value.Attrs().Nodes() {
			attrs[node.Key()] = &api.RichTextNodeAttr{
				Key:       node.Key(),
				Value:     node.Value(),
				UpdatedAt: toTimeTicket(node.UpdatedAt()),
			}
		}

		pbTextNode := &api.RichTextNode{
			Id:         toTextNodeID(textNode.ID()),
			Attributes: attrs,
			Value:      value.Value(),
			RemovedAt:  toTimeTicket(textNode.RemovedAt()),
		}

		if textNode.InsPrevID() != nil {
			pbTextNode.InsPrevId = toTextNodeID(textNode.InsPrevID())
		}

		pbTextNodes = append(pbTextNodes, pbTextNode)
	}
	return pbTextNodes
}

func toTextNodeID(id *json.RGATreeSplitNodeID) *api.TextNodeID {
	return &api.TextNodeID{
		CreatedAt: toTimeTicket(id.CreatedAt()),
		Offset:    int32(id.Offset()),
	}
}
