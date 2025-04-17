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

package converter

import (
	"fmt"
	"reflect"

	api "github.com/yorkie-team/yorkie/api/yorkie/v1"
	"github.com/yorkie-team/yorkie/pkg/document/crdt"
)

func ToPrimitiveStruct(primitive *crdt.Primitive) (*JSONPrimitiveStruct, error) {
	pbValueType, err := toValueType(primitive.ValueType())
	if err != nil {
		return nil, err
	}

	return &JSONPrimitiveStruct{
		JSONType:  pbValueType,
		ValueType: primitive.ValueType(),
		Value:     primitive.Value(),
	}, nil
}

func ToCounterStruct(counter *crdt.Counter) (*JSONCounterStruct, error) {
	pbCounterType, err := toCounterType(counter.ValueType())
	if err != nil {
		return nil, err
	}

	return &JSONCounterStruct{
		JSONType:  pbCounterType,
		ValueType: counter.ValueType(),
		Value:     counter.Value(),
	}, nil
}

func toArrayStruct(array *crdt.Array) (*JSONArrayStruct, error) {
	var elements []JSONStruct
	for _, elem := range array.Elements() {
		pbElem, err := ToJSONStruct(elem)
		if err != nil {
			return nil, err
		}
		elements = append(elements, pbElem)
	}
	return &JSONArrayStruct{
		JSONType: api.ValueType_VALUE_TYPE_JSON_ARRAY,
		Value:    elements,
	}, nil
}

func ToObjectStruct(object *crdt.Object) (*JSONObjectStruct, error) {
	fields := make(map[string]JSONStruct)
	for key, elem := range object.Members() {
		elemStruct, err := ToJSONStruct(elem)
		if err != nil {
			return nil, err
		}
		fields[key] = elemStruct
	}
	return &JSONObjectStruct{
		JSONType: api.ValueType_VALUE_TYPE_JSON_OBJECT,
		Value:    fields,
	}, nil
}

func ToTextStruct(text *crdt.Text) *JSONTextStruct {
	return &JSONTextStruct{
		JSONType: api.ValueType_VALUE_TYPE_TEXT,
		Value:    text.Marshal(),
	}
}

func ToTreeStruct(tree *crdt.Tree) *JSONTreeStruct {
	return &JSONTreeStruct{
		JSONType: api.ValueType_VALUE_TYPE_TREE,
		Value:    tree.Marshal(),
	}
}

func ToJSONStruct(elem crdt.Element) (JSONStruct, error) {
	switch elem := elem.(type) {
	case *crdt.Object:
		return ToObjectStruct(elem)
	case *crdt.Array:
		return toArrayStruct(elem)
	case *crdt.Primitive:
		return ToPrimitiveStruct(elem)
	case *crdt.Counter:
		return ToCounterStruct(elem)
	case *crdt.Text:
		return ToTextStruct(elem), nil
	case *crdt.Tree:
		return ToTreeStruct(elem), nil
	default:
		return nil, fmt.Errorf("%v: %w", reflect.TypeOf(elem), ErrUnsupportedElement)
	}
}
