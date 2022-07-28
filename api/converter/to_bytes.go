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
	"github.com/yorkie-team/yorkie/gen/go/yorkie/v1"
	"reflect"

	"github.com/gogo/protobuf/proto"

	"github.com/yorkie-team/yorkie/pkg/document/json"
)

// ObjectToBytes converts the given object to byte array.
func ObjectToBytes(obj *json.Object) ([]byte, error) {
	pbElem, err := toJSONElement(obj)
	if err != nil {
		return nil, err
	}

	bytes, err := proto.Marshal(pbElem)
	if err != nil {
		return nil, err
	}
	return bytes, nil
}

func toJSONElement(elem json.Element) (*v1.JSONElement, error) {
	switch elem := elem.(type) {
	case *json.Object:
		return toJSONObject(elem)
	case *json.Array:
		return toJSONArray(elem)
	case *json.Primitive:
		return toPrimitive(elem)
	case *json.Text:
		return toText(elem), nil
	case *json.RichText:
		return toRichText(elem), nil
	case *json.Counter:
		return toCounter(elem)
	default:
		return nil, fmt.Errorf("%v: %w", reflect.TypeOf(elem), ErrUnsupportedElement)
	}
}

func toJSONObject(obj *json.Object) (*v1.JSONElement, error) {
	pbRHTNodes, err := toRHTNodes(obj.RHTNodes())
	if err != nil {
		return nil, err
	}

	pbElem := &v1.JSONElement{
		Body: &v1.JSONElement_JsonObject{JsonObject: &v1.JSONElement_JSONObject{
			Nodes:     pbRHTNodes,
			CreatedAt: ToTimeTicket(obj.CreatedAt()),
			MovedAt:   ToTimeTicket(obj.MovedAt()),
			RemovedAt: ToTimeTicket(obj.RemovedAt()),
		}},
	}
	return pbElem, nil
}

func toJSONArray(arr *json.Array) (*v1.JSONElement, error) {
	pbRGANodes, err := toRGANodes(arr.RGANodes())
	if err != nil {
		return nil, err
	}

	pbElem := &v1.JSONElement{
		Body: &v1.JSONElement_JsonArray{JsonArray: &v1.JSONElement_JSONArray{
			Nodes:     pbRGANodes,
			CreatedAt: ToTimeTicket(arr.CreatedAt()),
			MovedAt:   ToTimeTicket(arr.MovedAt()),
			RemovedAt: ToTimeTicket(arr.RemovedAt()),
		}},
	}
	return pbElem, nil
}

func toPrimitive(primitive *json.Primitive) (*v1.JSONElement, error) {
	pbValueType, err := toValueType(primitive.ValueType())
	if err != nil {
		return nil, err
	}

	return &v1.JSONElement{
		Body: &v1.JSONElement_Primitive_{Primitive: &v1.JSONElement_Primitive{
			Type:      pbValueType,
			Value:     primitive.Bytes(),
			CreatedAt: ToTimeTicket(primitive.CreatedAt()),
			MovedAt:   ToTimeTicket(primitive.MovedAt()),
			RemovedAt: ToTimeTicket(primitive.RemovedAt()),
		}},
	}, nil
}

func toText(text *json.Text) *v1.JSONElement {
	return &v1.JSONElement{
		Body: &v1.JSONElement_Text_{Text: &v1.JSONElement_Text{
			Nodes:     toTextNodes(text.Nodes()),
			CreatedAt: ToTimeTicket(text.CreatedAt()),
			MovedAt:   ToTimeTicket(text.MovedAt()),
			RemovedAt: ToTimeTicket(text.RemovedAt()),
		}},
	}
}

func toRichText(text *json.RichText) *v1.JSONElement {
	return &v1.JSONElement{
		Body: &v1.JSONElement_RichText_{RichText: &v1.JSONElement_RichText{
			Nodes:     toRichTextNodes(text.Nodes()),
			CreatedAt: ToTimeTicket(text.CreatedAt()),
			MovedAt:   ToTimeTicket(text.MovedAt()),
			RemovedAt: ToTimeTicket(text.RemovedAt()),
		}},
	}
}

func toCounter(counter *json.Counter) (*v1.JSONElement, error) {
	pbCounterType, err := toCounterType(counter.ValueType())
	if err != nil {
		return nil, err
	}

	return &v1.JSONElement{
		Body: &v1.JSONElement_Counter_{Counter: &v1.JSONElement_Counter{
			Type:      pbCounterType,
			Value:     counter.Bytes(),
			CreatedAt: ToTimeTicket(counter.CreatedAt()),
			MovedAt:   ToTimeTicket(counter.MovedAt()),
			RemovedAt: ToTimeTicket(counter.RemovedAt()),
		}},
	}, nil
}

func toRHTNodes(rhtNodes []*json.RHTPQMapNode) ([]*v1.RHTNode, error) {
	var pbRHTNodes []*v1.RHTNode
	for _, rhtNode := range rhtNodes {
		pbElem, err := toJSONElement(rhtNode.Element())
		if err != nil {
			return nil, err
		}

		pbRHTNodes = append(pbRHTNodes, &v1.RHTNode{
			Key:     rhtNode.Key(),
			Element: pbElem,
		})
	}
	return pbRHTNodes, nil
}

func toRGANodes(rgaNodes []*json.RGATreeListNode) ([]*v1.RGANode, error) {
	var pbRGANodes []*v1.RGANode
	for _, rgaNode := range rgaNodes {
		pbElem, err := toJSONElement(rgaNode.Element())
		if err != nil {
			return nil, err
		}

		pbRGANodes = append(pbRGANodes, &v1.RGANode{
			Element: pbElem,
		})
	}
	return pbRGANodes, nil
}

func toTextNodes(textNodes []*json.RGATreeSplitNode[*json.TextValue]) []*v1.TextNode {
	var pbTextNodes []*v1.TextNode
	for _, textNode := range textNodes {
		pbTextNode := &v1.TextNode{
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

func toRichTextNodes(textNodes []*json.RGATreeSplitNode[*json.RichTextValue]) []*v1.RichTextNode {
	var pbTextNodes []*v1.RichTextNode
	for _, textNode := range textNodes {
		value := textNode.Value()

		attrs := make(map[string]*v1.RichTextNodeAttr)
		for _, node := range value.Attrs().Nodes() {
			attrs[node.Key()] = &v1.RichTextNodeAttr{
				Key:       node.Key(),
				Value:     node.Value(),
				UpdatedAt: ToTimeTicket(node.UpdatedAt()),
			}
		}

		pbTextNode := &v1.RichTextNode{
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

func toTextNodeID(id *json.RGATreeSplitNodeID) *v1.TextNodeID {
	return &v1.TextNodeID{
		CreatedAt: ToTimeTicket(id.CreatedAt()),
		Offset:    int32(id.Offset()),
	}
}
