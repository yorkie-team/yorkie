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
	ejson "encoding/json"
	"fmt"
	gotime "time"
	"unicode/utf16"

	"github.com/yorkie-team/yorkie/pkg/document/crdt"
	"github.com/yorkie-team/yorkie/pkg/document/json"
)

func SetObjFromJsonStruct(obj *json.Object, key string, value JSONStruct) error {
	switch j := value.(type) {
	case *JSONPrimitiveStruct:
		switch j.ValueType {
		case crdt.Null:
			obj.SetNull(key)
		case crdt.Boolean:
			obj.SetBool(key, j.Value.(bool))
		case crdt.Integer:
			obj.SetInteger(key, int(j.Value.(int32)))
		case crdt.Long:
			obj.SetLong(key, j.Value.(int64))
		case crdt.Double:
			obj.SetDouble(key, j.Value.(float64))
		case crdt.String:
			obj.SetString(key, j.Value.(string))
		case crdt.Bytes:
			obj.SetBytes(key, j.Value.([]byte))
		case crdt.Date:
			obj.SetDate(key, j.Value.(gotime.Time))
		}
	case *JSONCounterStruct:
		switch j.ValueType {
		case crdt.LongCnt:
			obj.SetNewCounter(key, crdt.LongCnt, j.Value.(int64))
		case crdt.IntegerCnt:
			obj.SetNewCounter(key, crdt.IntegerCnt, int(j.Value.(int32)))
		}
	case *JSONArrayStruct:
		arr := obj.SetNewArray(key)
		for _, elem := range j.Value {
			if err := AddArrFromJsonStruct(arr, elem); err != nil {
				return err
			}
		}
	case *JSONObjectStruct:
		o := obj.SetNewObject(key)
		for key, value := range j.Value {
			if err := SetObjFromJsonStruct(o, key, value); err != nil {
				return err
			}
		}
	case *JSONTextStruct:
		text := obj.SetNewText(key)
		if err := EditTextFromJSONStruct(*j, text); err != nil {
			return err
		}
	case *JSONTreeStruct:
		treeNode, err := GetTreeRootNodeFromJSONStruct(*j)
		if err != nil {
			return err
		}
		obj.SetNewTree(key, treeNode)
	default:
		return fmt.Errorf("unsupported JSONStruct type: %T", j)
	}
	return nil
}

func AddArrFromJsonStruct(arr *json.Array, value JSONStruct) error {
	switch j := value.(type) {
	case *JSONPrimitiveStruct:
		switch j.ValueType {
		case crdt.Null:
			arr.AddNull()
		case crdt.Boolean:
			arr.AddBool(j.Value.(bool))
		case crdt.Integer:
			arr.AddInteger(int(j.Value.(int32)))
		case crdt.Long:
			arr.AddLong(j.Value.(int64))
		case crdt.Double:
			arr.AddDouble(j.Value.(float64))
		case crdt.String:
			arr.AddString(j.Value.(string))
		case crdt.Bytes:
			arr.AddBytes(j.Value.([]byte))
		case crdt.Date:
			arr.AddDate(j.Value.(gotime.Time))
		}
	case *JSONCounterStruct:
		arr.AddNewCounter(j.ValueType, j.Value)
	case *JSONArrayStruct:
		a := arr.AddNewArray()
		for _, elem := range j.Value {
			if err := AddArrFromJsonStruct(a, elem); err != nil {
				return err
			}
		}
	case *JSONObjectStruct:
		o := arr.AddNewObject()
		for key, value := range j.Value {
			if err := SetObjFromJsonStruct(o, key, value); err != nil {
				return err
			}
		}
	case *JSONTextStruct:
		t := arr.AddNewText()
		if err := EditTextFromJSONStruct(*j, t); err != nil {
			return err
		}
	case *JSONTreeStruct:
		treeNode, err := GetTreeRootNodeFromJSONStruct(*j)
		if err != nil {
			return err
		}
		arr.AddNewTree(treeNode)
	default:
		return fmt.Errorf("unsupported JSONStruct type: %T", j)
	}
	return nil
}

func EditTextFromJSONStruct(j JSONTextStruct, text *json.Text) error {
	var chunks []interface{}
	if err := ejson.Unmarshal([]byte(j.Value), &chunks); err != nil {
		return fmt.Errorf("failed to parse text JSON: %w", err)
	}

	pos := 0
	for _, chunk := range chunks {
		chunkMap, ok := chunk.(map[string]interface{})
		if !ok {
			return fmt.Errorf("invalid text JSON: %+v", chunk)
		}
		value, ok := chunkMap["val"].(string)
		if !ok {
			return fmt.Errorf("invalid 'val' in text JSON: %+v", chunkMap)
		}

		if attrs, hasAttrs := chunkMap["attrs"]; hasAttrs {
			attrMap := attrs.(map[string]interface{})
			attributes := make(map[string]string)
			for attrKey, attrValue := range attrMap {
				attributes[attrKey] = attrValue.(string)
			}
			text.Edit(pos, pos, value, attributes)
		} else {
			text.Edit(pos, pos, value)
		}
		pos += len(utf16.Encode([]rune(value)))
	}
	return nil
}

func GetTreeRootNodeFromJSONStruct(j JSONTreeStruct) (*json.TreeNode, error) {
	var treeJSON json.TreeNode
	if err := ejson.Unmarshal([]byte(j.Value), &treeJSON); err != nil {
		return nil, fmt.Errorf("failed to parse tree JSON: %w", err)
	}

	// Create root node first
	rootNode := &json.TreeNode{
		Type:  treeJSON.Type,
		Value: treeJSON.Value,
	}
	if len(treeJSON.Children) > 0 {
		processChildren(rootNode, treeJSON.Children)
	}
	if len(treeJSON.Attributes) > 0 {
		processAttributes(rootNode, treeJSON.Attributes)
	}

	return rootNode, nil
}

func processChildren(node *json.TreeNode, children []json.TreeNode) {
	node.Children = make([]json.TreeNode, len(children))
	for i, child := range children {
		node.Children[i] = json.TreeNode{
			Type:  child.Type,
			Value: child.Value,
		}
		if len(child.Children) > 0 {
			processChildren(&node.Children[i], child.Children)
		}
		if len(child.Attributes) > 0 {
			processAttributes(&node.Children[i], child.Attributes)
		}
	}
}

func processAttributes(node *json.TreeNode, attrs map[string]string) {
	node.Attributes = make(map[string]string)
	for key, val := range attrs {
		node.Attributes[key] = fmt.Sprint(val)
	}
}
