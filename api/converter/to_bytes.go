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

	"github.com/gogo/protobuf/proto"

	"github.com/yorkie-team/yorkie/api"
	"github.com/yorkie-team/yorkie/pkg/document/json"
	"github.com/yorkie-team/yorkie/pkg/log"
)

// ObjectToBytes converts the given object to byte array.
func ObjectToBytes(obj *json.Object) ([]byte, error) {
	elem, err := toJSONElement(obj)
	if err != nil {
		return nil, err
	}

	bytes, err := proto.Marshal(elem)
	if err != nil {
		log.Logger.Error(err)
		return nil, err
	}
	return bytes, nil
}

func toJSONElement(elem json.Element) (*api.JSONElement, error) {
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
		return nil, fmt.Errorf("%s: %w", elem, ErrUnsupportedElement)
	}
}

func toJSONObject(obj *json.Object) (*api.JSONElement, error) {
	rhtNodes, err := toRHTNodes(obj.RHTNodes())
	if err != nil {
		return nil, err
	}

	pbElem := &api.JSONElement{
		Body: &api.JSONElement_Object_{Object: &api.JSONElement_Object{
			Nodes:     rhtNodes,
			CreatedAt: toTimeTicket(obj.CreatedAt()),
			MovedAt:   toTimeTicket(obj.MovedAt()),
			RemovedAt: toTimeTicket(obj.RemovedAt()),
		}},
	}
	return pbElem, nil
}

func toJSONArray(arr *json.Array) (*api.JSONElement, error) {
	rgaNodes, err := toRGANodes(arr.RGANodes())
	if err != nil {
		return nil, err
	}

	pbElem := &api.JSONElement{
		Body: &api.JSONElement_Array_{Array: &api.JSONElement_Array{
			Nodes:     rgaNodes,
			CreatedAt: toTimeTicket(arr.CreatedAt()),
			MovedAt:   toTimeTicket(arr.MovedAt()),
			RemovedAt: toTimeTicket(arr.RemovedAt()),
		}},
	}
	return pbElem, nil
}

func toPrimitive(primitive *json.Primitive) (*api.JSONElement, error) {
	valueType, err := toValueType(primitive.ValueType())
	if err != nil {
		return nil, err
	}

	return &api.JSONElement{
		Body: &api.JSONElement_Primitive_{Primitive: &api.JSONElement_Primitive{
			Type:      valueType,
			Value:     primitive.Bytes(),
			CreatedAt: toTimeTicket(primitive.CreatedAt()),
			MovedAt:   toTimeTicket(primitive.MovedAt()),
			RemovedAt: toTimeTicket(primitive.RemovedAt()),
		}},
	}, nil
}

func toText(text *json.Text) *api.JSONElement {
	return &api.JSONElement{
		Body: &api.JSONElement_Text_{Text: &api.JSONElement_Text{
			Nodes:     toTextNodes(text.Nodes()),
			CreatedAt: toTimeTicket(text.CreatedAt()),
			MovedAt:   toTimeTicket(text.MovedAt()),
			RemovedAt: toTimeTicket(text.RemovedAt()),
		}},
	}
}

func toRichText(text *json.RichText) *api.JSONElement {
	return &api.JSONElement{
		Body: &api.JSONElement_RichText_{RichText: &api.JSONElement_RichText{
			Nodes:     toRichTextNodes(text.Nodes()),
			CreatedAt: toTimeTicket(text.CreatedAt()),
			MovedAt:   toTimeTicket(text.MovedAt()),
			RemovedAt: toTimeTicket(text.RemovedAt()),
		}},
	}
}

func toCounter(counter *json.Counter) (*api.JSONElement, error) {
	counterType, err := toCounterType(counter.ValueType())
	if err != nil {
		return nil, err
	}

	return &api.JSONElement{
		Body: &api.JSONElement_Counter_{Counter: &api.JSONElement_Counter{
			Type:      counterType,
			Value:     counter.Bytes(),
			CreatedAt: toTimeTicket(counter.CreatedAt()),
			MovedAt:   toTimeTicket(counter.MovedAt()),
			RemovedAt: toTimeTicket(counter.RemovedAt()),
		}},
	}, nil
}

func toRHTNodes(rhtNodes []*json.RHTPQMapNode) ([]*api.RHTNode, error) {
	var pbRHTNodes []*api.RHTNode
	for _, rhtNode := range rhtNodes {
		elem, err := toJSONElement(rhtNode.Element())
		if err != nil {
			return nil, err
		}

		pbRHTNodes = append(pbRHTNodes, &api.RHTNode{
			Key:     rhtNode.Key(),
			Element: elem,
		})
	}
	return pbRHTNodes, nil
}

func toRGANodes(rgaNodes []*json.RGATreeListNode) ([]*api.RGANode, error) {
	var pbRGANodes []*api.RGANode
	for _, rgaNode := range rgaNodes {
		elem, err := toJSONElement(rgaNode.Element())
		if err != nil {
			return nil, err
		}

		pbRGANodes = append(pbRGANodes, &api.RGANode{
			Element: elem,
		})
	}
	return pbRGANodes, nil
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
