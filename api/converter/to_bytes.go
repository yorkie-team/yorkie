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
	"fmt"
	"reflect"

	"github.com/gogo/protobuf/proto"

	api "github.com/yorkie-team/yorkie/api/yorkie/v1"
	"github.com/yorkie-team/yorkie/pkg/document/crdt"
)

// ObjectToBytes converts the given object to byte array.
func ObjectToBytes(obj *crdt.Object) ([]byte, error) {
	pbElem, err := toJSONElement(obj)
	if err != nil {
		return nil, err
	}

	bytes, err := proto.Marshal(pbElem)
	if err != nil {
		return nil, fmt.Errorf("marshal JSON element to bytes: %w", err)
	}
	return bytes, nil
}

func toJSONElement(elem crdt.Element) (*api.JSONElement, error) {
	switch elem := elem.(type) {
	case *crdt.Object:
		return toJSONObject(elem)
	case *crdt.Array:
		return toJSONArray(elem)
	case *crdt.Primitive:
		return toPrimitive(elem)
	case *crdt.Text:
		return toText(elem), nil
	case *crdt.RichText:
		return toRichText(elem), nil
	case *crdt.Counter:
		return toCounter(elem)
	default:
		return nil, fmt.Errorf("%v: %w", reflect.TypeOf(elem), ErrUnsupportedElement)
	}
}

func toJSONObject(obj *crdt.Object) (*api.JSONElement, error) {
	pbRHTNodes, err := toRHTNodes(obj.RHTNodes())
	if err != nil {
		return nil, err
	}

	pbElem := &api.JSONElement{
		Body: &api.JSONElement_JsonObject{JsonObject: &api.JSONElement_JSONObject{
			Nodes:     pbRHTNodes,
			CreatedAt: ToTimeTicket(obj.CreatedAt()),
			MovedAt:   ToTimeTicket(obj.MovedAt()),
			RemovedAt: ToTimeTicket(obj.RemovedAt()),
		}},
	}
	return pbElem, nil
}

func toJSONArray(arr *crdt.Array) (*api.JSONElement, error) {
	pbRGANodes, err := toRGANodes(arr.RGANodes())
	if err != nil {
		return nil, err
	}

	pbElem := &api.JSONElement{
		Body: &api.JSONElement_JsonArray{JsonArray: &api.JSONElement_JSONArray{
			Nodes:     pbRGANodes,
			CreatedAt: ToTimeTicket(arr.CreatedAt()),
			MovedAt:   ToTimeTicket(arr.MovedAt()),
			RemovedAt: ToTimeTicket(arr.RemovedAt()),
		}},
	}
	return pbElem, nil
}

func toPrimitive(primitive *crdt.Primitive) (*api.JSONElement, error) {
	pbValueType, err := toValueType(primitive.ValueType())
	if err != nil {
		return nil, err
	}

	return &api.JSONElement{
		Body: &api.JSONElement_Primitive_{Primitive: &api.JSONElement_Primitive{
			Type:      pbValueType,
			Value:     primitive.Bytes(),
			CreatedAt: ToTimeTicket(primitive.CreatedAt()),
			MovedAt:   ToTimeTicket(primitive.MovedAt()),
			RemovedAt: ToTimeTicket(primitive.RemovedAt()),
		}},
	}, nil
}

func toText(text *crdt.Text) *api.JSONElement {
	return &api.JSONElement{
		Body: &api.JSONElement_Text_{Text: &api.JSONElement_Text{
			Nodes:     toTextNodes(text.Nodes()),
			CreatedAt: ToTimeTicket(text.CreatedAt()),
			MovedAt:   ToTimeTicket(text.MovedAt()),
			RemovedAt: ToTimeTicket(text.RemovedAt()),
		}},
	}
}

func toRichText(text *crdt.RichText) *api.JSONElement {
	return &api.JSONElement{
		Body: &api.JSONElement_RichText_{RichText: &api.JSONElement_RichText{
			Nodes:     toRichTextNodes(text.Nodes()),
			CreatedAt: ToTimeTicket(text.CreatedAt()),
			MovedAt:   ToTimeTicket(text.MovedAt()),
			RemovedAt: ToTimeTicket(text.RemovedAt()),
		}},
	}
}

func toCounter(counter *crdt.Counter) (*api.JSONElement, error) {
	pbCounterType, err := toCounterType(counter.ValueType())
	if err != nil {
		return nil, err
	}

	return &api.JSONElement{
		Body: &api.JSONElement_Counter_{Counter: &api.JSONElement_Counter{
			Type:      pbCounterType,
			Value:     counter.Bytes(),
			CreatedAt: ToTimeTicket(counter.CreatedAt()),
			MovedAt:   ToTimeTicket(counter.MovedAt()),
			RemovedAt: ToTimeTicket(counter.RemovedAt()),
		}},
	}, nil
}

func toRHTNodes(rhtNodes []*crdt.RHTPQMapNode) ([]*api.RHTNode, error) {
	var pbRHTNodes []*api.RHTNode
	for _, rhtNode := range rhtNodes {
		pbElem, err := toJSONElement(rhtNode.Element())
		if err != nil {
			return nil, err
		}

		pbRHTNodes = append(pbRHTNodes, &api.RHTNode{
			Key:     rhtNode.Key(),
			Element: pbElem,
		})
	}
	return pbRHTNodes, nil
}

func toRGANodes(rgaNodes []*crdt.RGATreeListNode) ([]*api.RGANode, error) {
	var pbRGANodes []*api.RGANode
	for _, rgaNode := range rgaNodes {
		pbElem, err := toJSONElement(rgaNode.Element())
		if err != nil {
			return nil, err
		}

		pbRGANodes = append(pbRGANodes, &api.RGANode{
			Element: pbElem,
		})
	}
	return pbRGANodes, nil
}

func toTextNodes(textNodes []*crdt.RGATreeSplitNode[*crdt.TextValue]) []*api.TextNode {
	var pbTextNodes []*api.TextNode
	for _, textNode := range textNodes {
		pbTextNode := &api.TextNode{
			Id:        toTextNodeID(textNode.ID()),
			Value:     textNode.String(),
			RemovedAt: ToTimeTicket(textNode.RemovedAt()),
		}

		if textNode.InsPrevID() != nil {
			pbTextNode.InsPrevId = toTextNodeID(textNode.InsPrevID())
		}

		pbTextNodes = append(pbTextNodes, pbTextNode)
	}
	return pbTextNodes
}

func toRichTextNodes(textNodes []*crdt.RGATreeSplitNode[*crdt.RichTextValue]) []*api.RichTextNode {
	var pbTextNodes []*api.RichTextNode
	for _, textNode := range textNodes {
		value := textNode.Value()

		attrs := make(map[string]*api.RichTextNodeAttr)
		for _, node := range value.Attrs().Nodes() {
			attrs[node.Key()] = &api.RichTextNodeAttr{
				Key:       node.Key(),
				Value:     node.Value(),
				UpdatedAt: ToTimeTicket(node.UpdatedAt()),
			}
		}

		pbTextNode := &api.RichTextNode{
			Id:         toTextNodeID(textNode.ID()),
			Attributes: attrs,
			Value:      value.Value(),
			RemovedAt:  ToTimeTicket(textNode.RemovedAt()),
		}

		if textNode.InsPrevID() != nil {
			pbTextNode.InsPrevId = toTextNodeID(textNode.InsPrevID())
		}

		pbTextNodes = append(pbTextNodes, pbTextNode)
	}
	return pbTextNodes
}

func toTextNodeID(id *crdt.RGATreeSplitNodeID) *api.TextNodeID {
	return &api.TextNodeID{
		CreatedAt: ToTimeTicket(id.CreatedAt()),
		Offset:    int32(id.Offset()),
	}
}
