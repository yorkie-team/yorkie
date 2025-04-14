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

package v067

import (
	"context"
	ejson "encoding/json"
	"fmt"
	"reflect"
	"strings"
	gotime "time"
	"unicode/utf16"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"

	"github.com/yorkie-team/yorkie/api/converter"
	"github.com/yorkie-team/yorkie/api/types"
	api "github.com/yorkie-team/yorkie/api/yorkie/v1"
	"github.com/yorkie-team/yorkie/pkg/document"
	"github.com/yorkie-team/yorkie/pkg/document/change"
	"github.com/yorkie-team/yorkie/pkg/document/crdt"
	"github.com/yorkie-team/yorkie/pkg/document/json"
	"github.com/yorkie-team/yorkie/pkg/document/key"
	"github.com/yorkie-team/yorkie/pkg/document/presence"
	"github.com/yorkie-team/yorkie/pkg/document/time"
	"github.com/yorkie-team/yorkie/server/backend/database"
)

// PruneAndRebaseToInitial creates an initial change from the current final state of documents
// and prunes their previous history.
func PruneAndRebaseToInitial(ctx context.Context, db *mongo.Client, databaseName string, batchSize int) error {
	docCol := db.Database(databaseName).Collection("documents")
	changeCol := db.Database(databaseName).Collection("changes")
	snapshotCol := db.Database(databaseName).Collection("snapshots")
	versionvectorsCol := db.Database(databaseName).Collection("versionvectors")

	// Track failed documents
	var failedDocs []struct {
		ProjectID types.ID
		DocID     types.ID
		DocKey    key.Key
	}
	var misMatchDocs []struct {
		ProjectID types.ID
		DocID     types.ID
		DocKey    key.Key
	}

	// 1. Query all documents
	cursor, err := docCol.Find(ctx, bson.M{})
	if err != nil {
		return fmt.Errorf("failed to find documents: %w", err)
	}
	defer cursor.Close(ctx)

	for cursor.Next(ctx) {
		var docInfo database.DocInfo
		if err := cursor.Decode(&docInfo); err != nil {
			return fmt.Errorf("failed to decode document: %w", err)
		}

		// 2. Build the final state of the current document: latest snapshot + changes
		// Query the most recent snapshot
		option := options.FindOne().SetSort(bson.M{
			"server_seq": -1,
		})
		result := snapshotCol.FindOne(ctx, bson.M{
			"project_id": docInfo.ProjectID,
			"doc_id":     docInfo.ID,
			"server_seq": bson.M{
				"$lte": docInfo.ServerSeq,
			},
		}, option)

		snapshotInfo := &database.SnapshotInfo{}
		if result.Err() == mongo.ErrNoDocuments {
			snapshotInfo.VersionVector = time.NewVersionVector()
		} else {
			if result.Err() != nil {
				return fmt.Errorf("failed to find snapshot: %w", result.Err())
			}

			if err := result.Decode(snapshotInfo); err != nil {
				return fmt.Errorf("failed to decode snapshot: %w", err)
			}
		}

		// Build document from snapshot
		doc, err := document.NewInternalDocumentFromSnapshot(
			docInfo.Key,
			snapshotInfo.ServerSeq,
			snapshotInfo.Lamport,
			snapshotInfo.VersionVector,
			snapshotInfo.Snapshot,
		)
		if err != nil {
			return err
		}

		fromSeq := snapshotInfo.ServerSeq + 1
		toSeq := docInfo.ServerSeq

		// Find and apply changes up to toSeq
		if fromSeq <= toSeq {
			cursor, err := changeCol.Find(ctx, bson.M{
				"project_id": docInfo.ProjectID,
				"doc_id":     docInfo.ID,
				"server_seq": bson.M{
					"$gte": fromSeq,
					"$lte": toSeq,
				},
			}, options.Find())
			if err != nil {
				return fmt.Errorf("failed to find changes: %w", err)
			}

			// TODO(chacha912): If the Snapshot is missing, we may have a very large
			// number of changes to read at once here. We need to split changes by a
			// certain size (e.g. 100) and read and gradually reflect it into the document.
			var infos []*database.ChangeInfo
			if err := cursor.All(ctx, &infos); err != nil {
				return fmt.Errorf("failed to fetch changes: %w", err)
			}

			var changes []*change.Change
		NextDocument:
			for _, info := range infos {
				c, err := safeToChange(info)
				if err != nil {
					fmt.Printf("🚨 Warning: Document %s failed to decode change: %v\n", docInfo.ID, err)
					failedDocs = append(failedDocs, struct {
						ProjectID types.ID
						DocID     types.ID
						DocKey    key.Key
					}{
						ProjectID: docInfo.ProjectID,
						DocID:     docInfo.ID,
						DocKey:    docInfo.Key,
					})
					break NextDocument
				}
				changes = append(changes, c)
			}

			if err := doc.ApplyChangePack(change.NewPack(
				docInfo.Key,
				change.InitialCheckpoint.NextServerSeq(docInfo.ServerSeq),
				changes,
				nil,
				nil,
			), false); err != nil {
				fmt.Printf("🚨 Warning: Document %s failed to apply changes: %v\n", docInfo.ID, err)
				failedDocs = append(failedDocs, struct {
					ProjectID types.ID
					DocID     types.ID
					DocKey    key.Key
				}{
					ProjectID: docInfo.ProjectID,
					DocID:     docInfo.ID,
					DocKey:    docInfo.Key,
				})
				continue
			}
		}

		// doc -> jsonStruct -> new rebase change
		// 3. Build jsonStruct from current document's final state
		jsonStruct, err := toJSONStruct(doc.RootObject())
		if err != nil {
			return err
		}

		// 4. Update new document with jsonStruct
		newDoc := document.New(docInfo.Key)
		err = newDoc.Update(func(root *json.Object, p *presence.Presence) error {
			for _, field := range jsonStruct.GetValue().([]*ObjectStruct) {
				setObjFromJsonStruct(root, field.Key, *field.Value)
			}
			return nil
		})
		if err != nil {
			return err
		}
		if newDoc.Marshal() != doc.Marshal() {
			misMatchDocs = append(misMatchDocs, struct {
				ProjectID types.ID
				DocID     types.ID
				DocKey    key.Key
			}{
				ProjectID: docInfo.ProjectID,
				DocID:     docInfo.ID,
				DocKey:    docInfo.Key,
			})

			fmt.Printf("🚨 Warning: Document %s content mismatch after rebuild\n", docInfo.ID)
			continue
		}

		// 5. Create new document's changepack
		changePack := newDoc.CreateChangePack()

		// 6. Save changes and delete previous data
		// 6-1. Delete old changes
		if _, err := changeCol.DeleteMany(ctx, bson.M{
			"project_id": docInfo.ProjectID,
			"doc_id":     docInfo.ID,
		}); err != nil {
			fmt.Printf("failed to delete old changes: %v", err)
			continue
		}

		// 6-2. Delete all snapshots
		if _, err := snapshotCol.DeleteMany(ctx, bson.M{
			"project_id": docInfo.ProjectID,
			"doc_id":     docInfo.ID,
		}); err != nil {
			fmt.Printf("failed to delete snapshots: %v", err)
			continue
		}

		// 6-3. Delete all versionvectors
		if _, err := versionvectorsCol.DeleteMany(ctx, bson.M{
			"project_id": docInfo.ProjectID,
			"doc_id":     docInfo.ID,
		}); err != nil {
			fmt.Printf("failed to delete versionvectors: %v", err)
			continue
		}

		// 6-4. Save new change
		if changePack.ChangesLen() > 0 {
			c := changePack.Changes[0]
			encodedOperations, err := database.EncodeOperations(c.Operations())
			if err != nil {
				fmt.Printf("failed to encode operations: %v", err)
				continue
			}
			changeInfo := &database.ChangeInfo{
				ID:            types.ID(primitive.NewObjectID().Hex()),
				ProjectID:     docInfo.ProjectID,
				DocID:         docInfo.ID,
				ServerSeq:     1, // Reset server sequence
				ClientSeq:     c.ID().ClientSeq(),
				Lamport:       c.ID().Lamport(),
				VersionVector: c.ID().VersionVector(),
				ActorID:       types.ID(c.ID().ActorID().String()),
				Operations:    encodedOperations,
			}
			if _, err := changeCol.InsertOne(ctx, changeInfo); err != nil {
				fmt.Printf("failed to insert new change: %v", err)
				continue
			}

			// 6-5. Reset docInfo serverseq
			// TODO(): Consider clearing clientInfo as well
			update := bson.M{
				"$set": bson.M{
					"server_seq": 1,
				},
			}
			if _, err := docCol.UpdateOne(ctx, bson.M{
				"project_id": docInfo.ProjectID,
				"key":        docInfo.Key,
			}, update); err != nil {
				fmt.Printf("failed to update document: %v", err)
				continue
			}
		} else {
			update := bson.M{
				"$set": bson.M{
					"server_seq": 0,
				},
			}
			if _, err := docCol.UpdateOne(ctx, bson.M{
				"project_id": docInfo.ProjectID,
				"key":        docInfo.Key,
			}, update); err != nil {
				fmt.Printf("failed to update document: %v", err)
				continue
			}
		}
	}

	// TODO(): Process failed documents separately - delete all their changes and reset to 0?
	fmt.Println("failed documents: ", len(failedDocs))
	fmt.Println("mismatch documents: ", len(misMatchDocs))

	return nil
}

func safeToChange(info *database.ChangeInfo) (c *change.Change, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("panic in ToChange(): %v", r)
		}
	}()

	return info.ToChange()
}

type JSONStruct interface {
	isJSONStruct()
	toTestString() string
	GetValue() interface{}
}

type JSONPrimitiveStruct struct {
	jsonType  api.ValueType
	valueType crdt.ValueType
	value     interface{}
}

type JSONCounterStruct struct {
	jsonType  api.ValueType
	valueType crdt.CounterType
	value     interface{}
}

type JSONArrayStruct struct {
	jsonType api.ValueType
	value    interface{}
}

type JSONObjectStruct struct {
	jsonType api.ValueType
	value    interface{}
}

type JSONTreeStruct struct {
	jsonType api.ValueType
	value    interface{}
}

type JSONTextStruct struct {
	jsonType api.ValueType
	value    interface{}
}

func (j JSONPrimitiveStruct) isJSONStruct() {}
func (j JSONCounterStruct) isJSONStruct()   {}
func (j JSONArrayStruct) isJSONStruct()     {}
func (j JSONObjectStruct) isJSONStruct()    {}
func (j JSONTreeStruct) isJSONStruct()      {}
func (j JSONTextStruct) isJSONStruct()      {}

func (j JSONPrimitiveStruct) GetValue() interface{} {
	return j.value
}
func (j JSONCounterStruct) GetValue() interface{} {
	return j.value
}
func (j JSONArrayStruct) GetValue() interface{} {
	return j.value
}
func (j JSONObjectStruct) GetValue() interface{} {
	return j.value
}
func (j JSONTreeStruct) GetValue() interface{} {
	return j.value
}
func (j JSONTextStruct) GetValue() interface{} {
	return j.value
}

func (j JSONPrimitiveStruct) toTestString() string {
	return fmt.Sprintf(
		"{jsonType: %s, valueType: %v, value: %v}",
		j.jsonType,
		j.valueType,
		j.value,
	)
}
func (j JSONCounterStruct) toTestString() string {
	return fmt.Sprintf(
		"{jsonType: %s, valueType: %v, value: %v}",
		j.jsonType,
		j.valueType,
		j.value,
	)
}
func (j JSONArrayStruct) toTestString() string {
	var elements []string
	values := j.value.([]*JSONStruct)
	for _, elem := range values {
		elements = append(elements, (*elem).toTestString())
	}
	return fmt.Sprintf(
		"{jsonType: %s, value: [%s]}",
		j.jsonType,
		strings.Join(elements, ", "),
	)
}
func (j JSONObjectStruct) toTestString() string {
	var pairs []string
	fields := j.value.([]*ObjectStruct)
	for _, field := range fields {
		pairs = append(pairs, fmt.Sprintf("%s: %s", field.Key, (*field.Value).toTestString()))
	}
	return fmt.Sprintf(
		"{jsonType: %s, value: {%s}}",
		j.jsonType,
		strings.Join(pairs, ", "),
	)
}
func (j JSONTreeStruct) toTestString() string {
	return fmt.Sprintf(
		"{jsonType: %s, value: %v}",
		j.jsonType,
		j.value,
	)
}
func (j JSONTextStruct) toTestString() string {
	return fmt.Sprintf(
		"{jsonType: %s, value: %v}",
		j.jsonType,
		j.value,
	)
}

func toPrimitive(primitive *crdt.Primitive) (*JSONPrimitiveStruct, error) {
	pbValueType, err := converter.ToValueType(primitive.ValueType())
	if err != nil {
		return nil, err
	}

	return &JSONPrimitiveStruct{
		jsonType:  pbValueType,
		valueType: primitive.ValueType(),
		value:     primitive.Value(),
	}, nil
}

func toCounter(counter *crdt.Counter) (*JSONCounterStruct, error) {
	pbCounterType, err := converter.ToCounterType(counter.ValueType())
	if err != nil {
		return nil, err
	}

	return &JSONCounterStruct{
		jsonType:  pbCounterType,
		valueType: counter.ValueType(),
		value:     counter.Value(),
	}, nil
}

func toArray(array *crdt.Array) (*JSONArrayStruct, error) {
	var elements []*JSONStruct
	for _, elem := range array.Elements() {
		pbElem, err := toJSONStruct(elem)
		if err != nil {
			return nil, err
		}
		elements = append(elements, &pbElem)
	}
	return &JSONArrayStruct{
		jsonType: api.ValueType_VALUE_TYPE_JSON_ARRAY,
		value:    elements,
	}, nil
}

type ObjectStruct struct {
	Key   string
	Value *JSONStruct
}

func toObject(object *crdt.Object) (*JSONObjectStruct, error) {
	var fields []*ObjectStruct
	for key, elem := range object.Members() {
		elemStruct, err := toJSONStruct(elem)
		if err != nil {
			return nil, err
		}
		fields = append(fields, &ObjectStruct{
			Key:   key,
			Value: &elemStruct,
		})
	}
	return &JSONObjectStruct{
		jsonType: api.ValueType_VALUE_TYPE_JSON_OBJECT,
		value:    fields,
	}, nil
}

func toText(text *crdt.Text) *JSONTextStruct {
	return &JSONTextStruct{
		jsonType: api.ValueType_VALUE_TYPE_TEXT,
		value:    text.Marshal(),
	}
}

func toTree(tree *crdt.Tree) *JSONTreeStruct {
	return &JSONTreeStruct{
		jsonType: api.ValueType_VALUE_TYPE_TREE,
		value:    tree.Marshal(),
	}
}

func toJSONStruct(elem crdt.Element) (JSONStruct, error) {
	switch elem := elem.(type) {
	case *crdt.Object:
		return toObject(elem)
	case *crdt.Array:
		return toArray(elem)
	case *crdt.Primitive:
		return toPrimitive(elem)
	case *crdt.Counter:
		return toCounter(elem)
	case *crdt.Text:
		return toText(elem), nil
	case *crdt.Tree:
		return toTree(elem), nil
	default:
		fmt.Println("❌ unsupported element: ", elem)
		return nil, fmt.Errorf("unsupported element: %v what", reflect.TypeOf(elem))
	}
}

func setObjFromJsonStruct(obj *json.Object, key string, value JSONStruct) error {
	switch j := value.(type) {
	case *JSONPrimitiveStruct:
		switch j.valueType {
		case crdt.Null:
			obj.SetNull(key)
		case crdt.Boolean:
			obj.SetBool(key, j.value.(bool))
		case crdt.Integer:
			obj.SetInteger(key, int(j.value.(int32)))
		case crdt.Long:
			obj.SetLong(key, j.value.(int64))
		case crdt.Double:
			obj.SetDouble(key, j.value.(float64))
		case crdt.String:
			obj.SetString(key, j.value.(string))
		case crdt.Bytes:
			obj.SetBytes(key, j.value.([]byte))
		case crdt.Date:
			obj.SetDate(key, j.value.(gotime.Time))
		}
	case *JSONCounterStruct:
		switch j.valueType {
		case crdt.LongCnt:
			obj.SetNewCounter(key, crdt.LongCnt, j.value.(int64))
		case crdt.IntegerCnt:
			obj.SetNewCounter(key, crdt.IntegerCnt, int(j.value.(int32)))
		}
	case *JSONArrayStruct:
		arr := obj.SetNewArray(key)
		for _, elem := range j.value.([]*JSONStruct) {
			addArrFromJsonStruct(arr, *elem)
		}
	case *JSONObjectStruct:
		o := obj.SetNewObject(key)
		for _, field := range j.value.([]*ObjectStruct) {
			setObjFromJsonStruct(o, field.Key, *field.Value)
		}
	case *JSONTextStruct:
		text := obj.SetNewText(key)
		editTextFromJSONStruct(*j, text)
	case *JSONTreeStruct:
		treeNode := getTreeRootNodeFromJSONStruct(*j)
		obj.SetNewTree(key, treeNode)
	default:
		return fmt.Errorf("unsupported JSONStruct type: %T", j)
	}
	return nil
}

func addArrFromJsonStruct(arr *json.Array, value JSONStruct) error {
	switch j := value.(type) {
	case *JSONPrimitiveStruct:
		switch j.valueType {
		case crdt.Null:
			arr.AddNull()
		case crdt.Boolean:
			arr.AddBool(j.value.(bool))
		case crdt.Integer:
			arr.AddInteger(int(j.value.(int32)))
		case crdt.Long:
			arr.AddLong(j.value.(int64))
		case crdt.Double:
			arr.AddDouble(j.value.(float64))
		case crdt.String:
			arr.AddString(j.value.(string))
		case crdt.Bytes:
			arr.AddBytes(j.value.([]byte))
		case crdt.Date:
			arr.AddDate(j.value.(gotime.Time))
		}
	case *JSONCounterStruct:
		arr.AddNewCounter(j.valueType, j.value)
	case *JSONArrayStruct:
		arr.AddNewArray()
		a := arr.GetArray(arr.Len() - 1)
		for _, elem := range j.value.([]*JSONStruct) {
			addArrFromJsonStruct(a, *elem)
		}
	case *JSONObjectStruct:
		arr.AddNewObject()
		o := arr.GetObject(arr.Len() - 1)
		for _, field := range j.value.([]*ObjectStruct) {
			setObjFromJsonStruct(o, field.Key, *field.Value)
		}
	case *JSONTextStruct:
		arr.AddNewText()
		t := arr.GetText(arr.Len() - 1)
		editTextFromJSONStruct(*j, t)
	case *JSONTreeStruct:
		// TODO(): implement
	default:
		return fmt.Errorf("unsupported JSONStruct type: %T", j)
	}
	return nil
}

type TreeNodeJSON struct {
	Type       string                 `json:"type"`
	Value      string                 `json:"value,omitempty"`
	Children   []TreeNodeJSON         `json:"children,omitempty"`
	Attributes map[string]interface{} `json:"attributes,omitempty"`
}

func getTreeRootNodeFromJSONStruct(j JSONTreeStruct) *json.TreeNode {
	var treeJSON TreeNodeJSON
	if err := ejson.Unmarshal([]byte(j.value.(string)), &treeJSON); err != nil {
		fmt.Printf("Failed to parse tree JSON: %v\n", err)
		return nil
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

	return rootNode
}

func processChildren(node *json.TreeNode, children []TreeNodeJSON) {
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

func processAttributes(node *json.TreeNode, attrs map[string]interface{}) {
	node.Attributes = make(map[string]string)
	for key, val := range attrs {
		node.Attributes[key] = fmt.Sprint(val)
	}
}

func editTextFromJSONStruct(j JSONTextStruct, text *json.Text) {
	var chunks []interface{}
	if err := ejson.Unmarshal([]byte(j.value.(string)), &chunks); err != nil {
		fmt.Printf("Failed to parse text JSON: %v\n", err)
	}

	pos := 0
	for _, chunk := range chunks {
		chunkMap := chunk.(map[string]interface{})
		value := chunkMap["val"].(string)

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
}
