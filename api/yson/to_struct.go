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

	"github.com/yorkie-team/yorkie/api/converter"
	"github.com/yorkie-team/yorkie/pkg/document/crdt"
)

func toPrimitiveStruct(primitive *crdt.Primitive) (*Primitive, error) {
	return &Primitive{
		ValueType: primitive.ValueType(),
		Value:     primitive.Value(),
	}, nil
}

func toCounterStruct(counter *crdt.Counter) (*Counter, error) {
	return &Counter{
		ValueType: counter.ValueType(),
		Value:     counter.Value(),
	}, nil
}

func toArrayStruct(array *crdt.Array) (*Array, error) {
	var elements []YSON
	for _, elem := range array.Elements() {
		pbElem, err := ToJSONStruct(elem)
		if err != nil {
			return nil, err
		}
		elements = append(elements, pbElem)
	}
	return &Array{
		Value: elements,
	}, nil
}

func toObjectStruct(object *crdt.Object) (*Object, error) {
	fields := make(map[string]YSON)
	for key, elem := range object.Members() {
		elemStruct, err := ToJSONStruct(elem)
		if err != nil {
			return nil, err
		}
		fields[key] = elemStruct
	}
	return &Object{
		Value: fields,
	}, nil
}

func toTextStruct(text *crdt.Text) *Text {
	return &Text{
		Value: text.Marshal(),
	}
}

func toTreeStruct(tree *crdt.Tree) *Tree {
	return &Tree{
		Value: tree.Marshal(),
	}
}

func ToJSONStruct(elem crdt.Element) (YSON, error) {
	switch elem := elem.(type) {
	case *crdt.Object:
		return toObjectStruct(elem)
	case *crdt.Array:
		return toArrayStruct(elem)
	case *crdt.Primitive:
		return toPrimitiveStruct(elem)
	case *crdt.Counter:
		return toCounterStruct(elem)
	case *crdt.Text:
		return toTextStruct(elem), nil
	case *crdt.Tree:
		return toTreeStruct(elem), nil
	default:
		return nil, fmt.Errorf("%v: %w", reflect.TypeOf(elem), converter.ErrUnsupportedElement)
	}
}
