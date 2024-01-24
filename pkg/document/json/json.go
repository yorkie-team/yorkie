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

// Package json provides the JSON document implementation.
package json

import (
	"reflect"
	"strings"
	gotime "time"
	"unicode"

	"github.com/yorkie-team/yorkie/pkg/document/change"
	"github.com/yorkie-team/yorkie/pkg/document/crdt"
	"github.com/yorkie-team/yorkie/pkg/document/time"
)

// tagOptions is the string following a comma in a struct field's "yorkie"
// tag, or the empty string.
type tagOptions string

func toOriginal(elem crdt.Element) crdt.Element {
	switch elem := elem.(type) {
	case *Object:
		return elem.Object
	case *Array:
		return elem.Array
	case *Text:
		return elem.Text
	case *Counter:
		return elem.Counter
	case *Tree:
		return elem.Tree
	case *crdt.Primitive:
		return elem
	}
	panic("unsupported type")
}

// toElement converts crdt.Element to the corresponding json.Element.
func toElement(ctx *change.Context, elem crdt.Element) crdt.Element {
	switch elem := elem.(type) {
	case *crdt.Object:
		return NewObject(ctx, elem)
	case *crdt.Array:
		return NewArray(ctx, elem)
	case *crdt.Text:
		text := NewText()
		return text.Initialize(ctx, elem)
	case *crdt.Counter:
		counter := NewCounter(elem.Value(), elem.ValueType())
		return counter.Initialize(ctx, elem)
	case *crdt.Tree:
		tree := NewTree()
		return tree.Initialize(ctx, elem)
	case *crdt.Primitive:
		return elem
	}
	panic("unsupported type")
}

// buildCRDTElement builds crdt.Element from the given value.
func buildCRDTElement(
	context *change.Context,
	value any,
	ticket *time.Ticket,
) crdt.Element {
	// 01. The type of the given value is one of the basic types.
	switch elem := value.(type) {
	case nil, string, int, int32, int64, float32, float64, []byte, bool, gotime.Time:
		primitive, err := crdt.NewPrimitive(elem, ticket)
		if err != nil {
			panic(err)
		}

		return primitive
	case Tree:
		return crdt.NewTree(buildRoot(context, elem.initialRoot, ticket), ticket)
	case Text:
		return crdt.NewText(crdt.NewRGATreeSplit(crdt.InitialTextNode()), ticket)
	case Counter:
		if elem.value == nil {
			elem.value = 0
		}
		counter, err := crdt.NewCounter(elem.valueType, elem.value, ticket)
		if err != nil {
			panic(err)
		}

		return counter
	case Object:
		// NOTE(highcloud100): This case only occurs when the given struct's element is json.Object.
		// For example, the given struct is `type Foo struct { Bar Object }`.
		// User can't initialize the json.Object directly. so just return the empty json.Object.
		return crdt.NewObject(crdt.NewElementRHT(), ticket, nil)
	case map[string]any:
		return crdt.NewObject(crdt.NewElementRHT(), ticket, buildObjectMembers(context, elem))
	case reflect.Value:
		// NOTE(highcloud100): This case only occurs when struct's reflect.Value is given.
		// BuildArrayElements only can throw the arbitrary struct as reflect.Value type to this function.
		if elem.Type().Kind() != reflect.Struct {
			break
		}
		return crdt.NewObject(crdt.NewElementRHT(), ticket, buildObjectMembers(context, valueToMap(elem)))
	}

	// 02. The type of the given value is user defined struct or array.
	switch reflect.ValueOf(value).Kind() {
	case reflect.Slice:
		return crdt.NewArray(crdt.NewRGATreeList(), ticket, buildArrayElements(context, value))
	case reflect.Array:
		// TODO(highcloud100): For now, buildArrayElements only accepts slice type.
		// We need to support array type later to avoid copying the slice.
		length := reflect.ValueOf(value).Len()
		slice := reflect.MakeSlice(reflect.SliceOf(reflect.ValueOf(value).Type().Elem()), length, length)
		reflect.Copy(slice, reflect.ValueOf(value))
		return crdt.NewArray(crdt.NewRGATreeList(), ticket, buildArrayElements(context, slice.Interface()))
	case reflect.Pointer:
		val := reflect.ValueOf(value)
		if val.IsNil() || !val.Elem().CanInterface() {
			return buildCRDTElement(context, nil, ticket)
		}
		return buildCRDTElement(context, val.Elem().Interface(), ticket)
	case reflect.Struct:
		return buildCRDTElement(context, reflect.ValueOf(value), ticket)
	default:
		panic("unsupported type")
	}
}

// valueToMap converts reflect.Value(struct) to map[string]interface{}
// except the field that has the tag "yorkie:-" or omitEmpty option and the
// field that is unexported.
// NOTE(highcloud100): This code referred to the "encoding/json" implementation.
func valueToMap(value reflect.Value) map[string]interface{} {
	json := make(map[string]interface{})
	for i := 0; i < value.NumField(); i++ {
		field := value.Field(i)
		fieldType := value.Type().Field(i)
		tag := fieldType.Tag.Get("yorkie")

		if !field.CanInterface() || tag == "-" {
			continue
		}

		name, options := parseTag(tag)
		if !isValidTag(name) {
			name = ""
		}

		if options.Contains("omitEmpty") && isEmptyValue(field) {
			continue
		}

		if name == "" {
			name = fieldType.Name
		}

		json[name] = value.Field(i).Interface()
	}
	return json
}

// parseTag parses the given tag to (name, option).
// This code referred to the "encoding/json/tags.go" implementation.
func parseTag(tag string) (string, tagOptions) {
	tag, opt, _ := strings.Cut(tag, ",")
	return tag, tagOptions(opt)
}

// isValidTag returns whether the given tag is valid.
// This code referred to the "encoding/json" implementation.
func isValidTag(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		switch {
		case strings.ContainsRune("!#$%&()*+-./:;<=>?@[]^_{|}~ ", c):
		// Backslash and quote chars are reserved, but
		// otherwise any punctuation chars are allowed
		// in a tag name.
		case !unicode.IsLetter(c) && !unicode.IsDigit(c):
			return false
		}
	}
	return true
}

// Contains reports whether the given option is contained in the tag options.
// Blank spaces in options are ignored by Trim.
// This code referred to the "encoding/json/tags.go" implementation.
func (o tagOptions) Contains(optionName string) bool {
	if len(o) == 0 {
		return false
	}
	s := string(o)
	for s != "" {
		var name string
		name, s, _ = strings.Cut(s, ",")
		if strings.Trim(name, " ") == optionName {
			return true
		}
	}
	return false
}

// isEmptyValue reports whether the given value is empty.
// This code referred to the "encoding/json/encode.go" implementation.
func isEmptyValue(v reflect.Value) bool {
	switch v.Kind() {
	case reflect.Array, reflect.Map, reflect.Slice, reflect.String:
		return v.Len() == 0
	case reflect.Bool:
		return v.Bool() == false
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return v.Int() == 0
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return v.Uint() == 0
	case reflect.Float32, reflect.Float64:
		return v.Float() == 0
	case reflect.Interface, reflect.Pointer:
		return v.IsNil()
	case reflect.Struct:
		for i := 0; i < v.NumField(); i++ {
			if !isEmptyValue(v.Field(i)) {
				return false
			}
		}
		return true
	}
	return false
}
