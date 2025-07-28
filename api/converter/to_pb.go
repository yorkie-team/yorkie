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

	"google.golang.org/protobuf/types/known/timestamppb"
	"google.golang.org/protobuf/types/known/wrapperspb"

	"github.com/yorkie-team/yorkie/api/types"
	"github.com/yorkie-team/yorkie/api/types/events"
	api "github.com/yorkie-team/yorkie/api/yorkie/v1"
	"github.com/yorkie-team/yorkie/pkg/document/change"
	"github.com/yorkie-team/yorkie/pkg/document/crdt"
	"github.com/yorkie-team/yorkie/pkg/document/innerpresence"
	"github.com/yorkie-team/yorkie/pkg/document/operations"
	"github.com/yorkie-team/yorkie/pkg/document/time"
	"github.com/yorkie-team/yorkie/pkg/resource"
)

// ToUser converts the given model format to Protobuf format.
func ToUser(user *types.User) *api.User {
	return &api.User{
		Id:           user.ID.String(),
		AuthProvider: user.AuthProvider,
		Username:     user.Username,
		CreatedAt:    timestamppb.New(user.CreatedAt),
	}
}

// ToProjects converts the given model to Protobuf.
func ToProjects(projects []*types.Project) []*api.Project {
	var pbProjects []*api.Project
	for _, project := range projects {
		pbProjects = append(pbProjects, ToProject(project))
	}

	return pbProjects
}

// ToProject converts the given model to Protobuf.
func ToProject(project *types.Project) *api.Project {
	return &api.Project{
		Id:                        project.ID.String(),
		Name:                      project.Name,
		AuthWebhookUrl:            project.AuthWebhookURL,
		AuthWebhookMethods:        project.AuthWebhookMethods,
		EventWebhookUrl:           project.EventWebhookURL,
		EventWebhookEvents:        project.EventWebhookEvents,
		ClientDeactivateThreshold: project.ClientDeactivateThreshold,
		MaxSubscribersPerDocument: int32(project.MaxSubscribersPerDocument),
		MaxAttachmentsPerDocument: int32(project.MaxAttachmentsPerDocument),
		MaxSizePerDocument:        int32(project.MaxSizePerDocument),
		AllowedOrigins:            project.AllowedOrigins,
		PublicKey:                 project.PublicKey,
		SecretKey:                 project.SecretKey,
		CreatedAt:                 timestamppb.New(project.CreatedAt),
		UpdatedAt:                 timestamppb.New(project.UpdatedAt),
	}
}

// ToMetricPoints converts the given model to Protobuf.
func ToMetricPoints(activeUsers []types.MetricPoint) []*api.MetricPoint {
	var pbActiveUsers []*api.MetricPoint
	for _, activeUser := range activeUsers {
		pbActiveUsers = append(pbActiveUsers, &api.MetricPoint{
			Timestamp: activeUser.Time,
			Value:     int32(activeUser.Value),
		})
	}
	return pbActiveUsers
}

// ToDocumentSummaries converts the given model to Protobuf.
func ToDocumentSummaries(summaries []*types.DocumentSummary) []*api.DocumentSummary {
	var pbSummaries []*api.DocumentSummary
	for _, summary := range summaries {
		pbSummaries = append(pbSummaries, ToDocumentSummary(summary))
	}
	return pbSummaries
}

// ToDocumentSummary converts the given model to Protobuf format.
func ToDocumentSummary(summary *types.DocumentSummary) *api.DocumentSummary {
	pbSummary := &api.DocumentSummary{
		Id:              summary.ID.String(),
		Key:             summary.Key.String(),
		CreatedAt:       timestamppb.New(summary.CreatedAt),
		AccessedAt:      timestamppb.New(summary.AccessedAt),
		UpdatedAt:       timestamppb.New(summary.UpdatedAt),
		Root:            summary.Root,
		AttachedClients: int32(summary.AttachedClients),
		DocumentSize:    ToDocSize(summary.DocSize),
		SchemaKey:       summary.SchemaKey,
	}

	if summary.Presences != nil {
		pbSummary.Presences = ToPresences(summary.Presences)
	}

	return pbSummary
}

// ToPresences converts the given model to Protobuf format.
func ToPresences(presences map[string]innerpresence.Presence) map[string]*api.Presence {
	pbPresences := make(map[string]*api.Presence)
	for k, v := range presences {
		pbPresences[k] = ToPresence(v)
	}
	return pbPresences
}

// ToPresence converts the given model to Protobuf format.
func ToPresence(p innerpresence.Presence) *api.Presence {
	if p == nil {
		return nil
	}

	return &api.Presence{
		Data: p,
	}
}

// ToPresenceChange converts the given model to Protobuf format.
func ToPresenceChange(p *innerpresence.Change) *api.PresenceChange {
	if p == nil {
		return nil
	}

	switch p.ChangeType {
	case innerpresence.Put:
		return &api.PresenceChange{
			Type:     api.PresenceChange_CHANGE_TYPE_PUT,
			Presence: &api.Presence{Data: p.Presence},
		}
	case innerpresence.Clear:
		return &api.PresenceChange{
			Type: api.PresenceChange_CHANGE_TYPE_CLEAR,
		}
	}
	return &api.PresenceChange{
		Type: api.PresenceChange_CHANGE_TYPE_UNSPECIFIED,
	}
}

// ToChangePack converts the given model format to Protobuf format.
func ToChangePack(pack *change.Pack) (*api.ChangePack, error) {
	pbChanges, err := ToChanges(pack.Changes)
	if err != nil {
		return nil, err
	}

	pbVersionVector, err := ToVersionVector(pack.VersionVector)
	if err != nil {
		return nil, err
	}

	return &api.ChangePack{
		DocumentKey:   pack.DocumentKey.String(),
		Checkpoint:    ToCheckpoint(pack.Checkpoint),
		Changes:       pbChanges,
		Snapshot:      pack.Snapshot,
		VersionVector: pbVersionVector,
		IsRemoved:     pack.IsRemoved,
	}, nil
}

// ToCheckpoint converts the given model format to Protobuf format.
func ToCheckpoint(cp change.Checkpoint) *api.Checkpoint {
	return &api.Checkpoint{
		ServerSeq: cp.ServerSeq,
		ClientSeq: cp.ClientSeq,
	}
}

// ToChangeID converts the given model format to Protobuf format.
func ToChangeID(id change.ID) (*api.ChangeID, error) {
	pbVersionVector, err := ToVersionVector(id.VersionVector())
	if err != nil {
		return nil, err
	}
	return &api.ChangeID{
		ClientSeq:     id.ClientSeq(),
		ServerSeq:     id.ServerSeq(),
		Lamport:       id.Lamport(),
		ActorId:       id.ActorID().Bytes(),
		VersionVector: pbVersionVector,
	}, nil
}

// ToVersionVector converts the given model format to Protobuf format.
func ToVersionVector(vector time.VersionVector) (*api.VersionVector, error) {
	pbVersionVector := make(map[string]int64)
	for actor, clock := range vector {
		id, err := time.ActorIDFromBytes(actor[:])
		if err != nil {
			return nil, err
		}

		pbVersionVector[id.StringBase64()] = clock
	}

	return &api.VersionVector{
		Vector: pbVersionVector,
	}, nil
}

// ToDocEventType converts the given model format to Protobuf format.
func ToDocEventType(eventType events.DocEventType) (api.DocEventType, error) {
	switch eventType {
	case events.DocChanged:
		return api.DocEventType_DOC_EVENT_TYPE_DOCUMENT_CHANGED, nil
	case events.DocWatched:
		return api.DocEventType_DOC_EVENT_TYPE_DOCUMENT_WATCHED, nil
	case events.DocUnwatched:
		return api.DocEventType_DOC_EVENT_TYPE_DOCUMENT_UNWATCHED, nil
	case events.DocBroadcast:
		return api.DocEventType_DOC_EVENT_TYPE_DOCUMENT_BROADCAST, nil
	default:
		return 0, fmt.Errorf("%s: %w", eventType, ErrUnsupportedEventType)
	}
}

func ToDataSize(dataSize resource.DataSize) *api.DataSize {
	return &api.DataSize{
		Data: int32(dataSize.Data),
		Meta: int32(dataSize.Meta),
	}
}

func ToDocSize(docSize resource.DocSize) *api.DocSize {
	return &api.DocSize{
		Live: ToDataSize(docSize.Live),
		Gc:   ToDataSize(docSize.GC),
	}
}

// ToOperations converts the given model format to Protobuf format.
func ToOperations(ops []operations.Operation) ([]*api.Operation, error) {
	var pbOperations []*api.Operation

	for _, o := range ops {
		pbOperation := &api.Operation{}
		var err error
		switch op := o.(type) {
		case *operations.Set:
			pbOperation.Body, err = toSet(op)
		case *operations.Add:
			pbOperation.Body, err = toAdd(op)
		case *operations.Move:
			pbOperation.Body, err = toMove(op)
		case *operations.Remove:
			pbOperation.Body, err = toRemove(op)
		case *operations.Edit:
			pbOperation.Body, err = toEdit(op)
		case *operations.Style:
			pbOperation.Body, err = toStyle(op)
		case *operations.Increase:
			pbOperation.Body, err = toIncrease(op)
		case *operations.TreeEdit:
			pbOperation.Body, err = toTreeEdit(op)
		case *operations.TreeStyle:
			pbOperation.Body, err = toTreeStyle(op)
		case *operations.ArraySet:
			pbOperation.Body, err = toArraySet(op)
		default:
			return nil, ErrUnsupportedOperation
		}
		if err != nil {
			return nil, err
		}
		pbOperations = append(pbOperations, pbOperation)
	}

	return pbOperations, nil
}

// ToTimeTicket converts the given model format to Protobuf format.
func ToTimeTicket(ticket *time.Ticket) *api.TimeTicket {
	if ticket == nil {
		return nil
	}

	return &api.TimeTicket{
		Lamport:   ticket.Lamport(),
		Delimiter: ticket.Delimiter(),
		ActorId:   ticket.ActorIDBytes(),
	}
}

// ToChanges converts the given model format to Protobuf format.
func ToChanges(changes []*change.Change) ([]*api.Change, error) {
	var pbChanges []*api.Change

	for _, c := range changes {
		pbOperations, err := ToOperations(c.Operations())
		if err != nil {
			return nil, err
		}

		pbChangeID, err := ToChangeID(c.ID())
		if err != nil {
			return nil, err
		}

		pbChanges = append(pbChanges, &api.Change{
			Id:             pbChangeID,
			Message:        c.Message(),
			Operations:     pbOperations,
			PresenceChange: ToPresenceChange(c.PresenceChange()),
		})
	}

	return pbChanges, nil
}

func toSet(set *operations.Set) (*api.Operation_Set_, error) {
	pbElem, err := toJSONElementSimple(set.Value())
	if err != nil {
		return nil, err
	}

	return &api.Operation_Set_{
		Set: &api.Operation_Set{
			ParentCreatedAt: ToTimeTicket(set.ParentCreatedAt()),
			Key:             set.Key(),
			Value:           pbElem,
			ExecutedAt:      ToTimeTicket(set.ExecutedAt()),
		},
	}, nil
}

func toAdd(add *operations.Add) (*api.Operation_Add_, error) {
	pbElem, err := toJSONElementSimple(add.Value())
	if err != nil {
		return nil, err
	}

	return &api.Operation_Add_{
		Add: &api.Operation_Add{
			ParentCreatedAt: ToTimeTicket(add.ParentCreatedAt()),
			PrevCreatedAt:   ToTimeTicket(add.PrevCreatedAt()),
			Value:           pbElem,
			ExecutedAt:      ToTimeTicket(add.ExecutedAt()),
		},
	}, nil
}

func toMove(move *operations.Move) (*api.Operation_Move_, error) {
	return &api.Operation_Move_{
		Move: &api.Operation_Move{
			ParentCreatedAt: ToTimeTicket(move.ParentCreatedAt()),
			PrevCreatedAt:   ToTimeTicket(move.PrevCreatedAt()),
			CreatedAt:       ToTimeTicket(move.CreatedAt()),
			ExecutedAt:      ToTimeTicket(move.ExecutedAt()),
		},
	}, nil
}

func toRemove(remove *operations.Remove) (*api.Operation_Remove_, error) {
	return &api.Operation_Remove_{
		Remove: &api.Operation_Remove{
			ParentCreatedAt: ToTimeTicket(remove.ParentCreatedAt()),
			CreatedAt:       ToTimeTicket(remove.CreatedAt()),
			ExecutedAt:      ToTimeTicket(remove.ExecutedAt()),
		},
	}, nil
}

func toEdit(e *operations.Edit) (*api.Operation_Edit_, error) {
	return &api.Operation_Edit_{
		Edit: &api.Operation_Edit{
			ParentCreatedAt: ToTimeTicket(e.ParentCreatedAt()),
			From:            toTextNodePos(e.From()),
			To:              toTextNodePos(e.To()),
			Content:         e.Content(),
			Attributes:      e.Attributes(),
			ExecutedAt:      ToTimeTicket(e.ExecutedAt()),
		},
	}, nil
}

func toStyle(style *operations.Style) (*api.Operation_Style_, error) {
	return &api.Operation_Style_{
		Style: &api.Operation_Style{
			ParentCreatedAt: ToTimeTicket(style.ParentCreatedAt()),
			From:            toTextNodePos(style.From()),
			To:              toTextNodePos(style.To()),
			Attributes:      style.Attributes(),
			ExecutedAt:      ToTimeTicket(style.ExecutedAt()),
		},
	}, nil
}

func toIncrease(increase *operations.Increase) (*api.Operation_Increase_, error) {
	pbElem, err := toJSONElementSimple(increase.Value())
	if err != nil {
		return nil, err
	}

	return &api.Operation_Increase_{
		Increase: &api.Operation_Increase{
			ParentCreatedAt: ToTimeTicket(increase.ParentCreatedAt()),
			Value:           pbElem,
			ExecutedAt:      ToTimeTicket(increase.ExecutedAt()),
		},
	}, nil
}

func toTreeEdit(e *operations.TreeEdit) (*api.Operation_TreeEdit_, error) {
	return &api.Operation_TreeEdit_{
		TreeEdit: &api.Operation_TreeEdit{
			ParentCreatedAt: ToTimeTicket(e.ParentCreatedAt()),
			From:            toTreePos(e.FromPos()),
			To:              toTreePos(e.ToPos()),
			Contents:        ToTreeNodesWhenEdit(e.Contents()),
			SplitLevel:      int32(e.SplitLevel()),
			ExecutedAt:      ToTimeTicket(e.ExecutedAt()),
		},
	}, nil
}

func toTreeStyle(style *operations.TreeStyle) (*api.Operation_TreeStyle_, error) {
	return &api.Operation_TreeStyle_{
		TreeStyle: &api.Operation_TreeStyle{
			ParentCreatedAt:    ToTimeTicket(style.ParentCreatedAt()),
			From:               toTreePos(style.FromPos()),
			To:                 toTreePos(style.ToPos()),
			Attributes:         style.Attributes(),
			ExecutedAt:         ToTimeTicket(style.ExecutedAt()),
			AttributesToRemove: style.AttributesToRemove(),
		},
	}, nil
}

func toArraySet(setByIndex *operations.ArraySet) (*api.Operation_ArraySet_, error) {
	pbElem, err := toJSONElementSimple(setByIndex.Value())
	if err != nil {
		return nil, err
	}

	return &api.Operation_ArraySet_{
		ArraySet: &api.Operation_ArraySet{
			ParentCreatedAt: ToTimeTicket(setByIndex.ParentCreatedAt()),
			CreatedAt:       ToTimeTicket(setByIndex.CreatedAt()),
			Value:           pbElem,
			ExecutedAt:      ToTimeTicket(setByIndex.ExecutedAt()),
		},
	}, nil
}

func toJSONElementSimple(elem crdt.Element) (*api.JSONElementSimple, error) {
	switch elem := elem.(type) {
	case *crdt.Object:
		bytes, err := ObjectToBytes(elem)
		if err != nil {
			return nil, err
		}
		return &api.JSONElementSimple{
			Type:      api.ValueType_VALUE_TYPE_JSON_OBJECT,
			CreatedAt: ToTimeTicket(elem.CreatedAt()),
			Value:     bytes,
		}, nil
	case *crdt.Array:
		bytes, err := ArrayToBytes(elem)
		if err != nil {
			return nil, err
		}
		return &api.JSONElementSimple{
			Type:      api.ValueType_VALUE_TYPE_JSON_ARRAY,
			CreatedAt: ToTimeTicket(elem.CreatedAt()),
			Value:     bytes,
		}, nil
	case *crdt.Primitive:
		pbValueType, err := toValueType(elem.ValueType())
		if err != nil {
			return nil, err
		}
		return &api.JSONElementSimple{
			Type:      pbValueType,
			CreatedAt: ToTimeTicket(elem.CreatedAt()),
			Value:     elem.Bytes(),
		}, nil
	case *crdt.Text:
		return &api.JSONElementSimple{
			Type:      api.ValueType_VALUE_TYPE_TEXT,
			CreatedAt: ToTimeTicket(elem.CreatedAt()),
		}, nil
	case *crdt.Counter:
		pbCounterType, err := toCounterType(elem.ValueType())
		if err != nil {
			return nil, err
		}
		counterValue, err := elem.Bytes()
		if err != nil {
			return nil, err
		}

		return &api.JSONElementSimple{
			Type:      pbCounterType,
			CreatedAt: ToTimeTicket(elem.CreatedAt()),
			Value:     counterValue,
		}, nil
	case *crdt.Tree:
		bytes, err := TreeToBytes(elem)
		if err != nil {
			return nil, err
		}
		return &api.JSONElementSimple{
			Type:      api.ValueType_VALUE_TYPE_TREE,
			CreatedAt: ToTimeTicket(elem.CreatedAt()),
			Value:     bytes,
		}, nil
	}

	return nil, fmt.Errorf("%v, %w", reflect.TypeOf(elem), ErrUnsupportedElement)
}

func toTextNodePos(pos *crdt.RGATreeSplitNodePos) *api.TextNodePos {
	return &api.TextNodePos{
		CreatedAt:      ToTimeTicket(pos.ID().CreatedAt()),
		Offset:         int32(pos.ID().Offset()),
		RelativeOffset: int32(pos.RelativeOffset()),
	}
}

func toValueType(valueType crdt.ValueType) (api.ValueType, error) {
	switch valueType {
	case crdt.Null:
		return api.ValueType_VALUE_TYPE_NULL, nil
	case crdt.Boolean:
		return api.ValueType_VALUE_TYPE_BOOLEAN, nil
	case crdt.Integer:
		return api.ValueType_VALUE_TYPE_INTEGER, nil
	case crdt.Long:
		return api.ValueType_VALUE_TYPE_LONG, nil
	case crdt.Double:
		return api.ValueType_VALUE_TYPE_DOUBLE, nil
	case crdt.String:
		return api.ValueType_VALUE_TYPE_STRING, nil
	case crdt.Bytes:
		return api.ValueType_VALUE_TYPE_BYTES, nil
	case crdt.Date:
		return api.ValueType_VALUE_TYPE_DATE, nil
	}

	return 0, fmt.Errorf("%d, %w", valueType, ErrUnsupportedValueType)
}

func toCounterType(valueType crdt.CounterType) (api.ValueType, error) {
	switch valueType {
	case crdt.IntegerCnt:
		return api.ValueType_VALUE_TYPE_INTEGER_CNT, nil
	case crdt.LongCnt:
		return api.ValueType_VALUE_TYPE_LONG_CNT, nil
	}

	return 0, fmt.Errorf("%d, %w", valueType, ErrUnsupportedCounterType)
}

// ToUpdatableProjectFields converts the given model format to Protobuf format.
func ToUpdatableProjectFields(fields *types.UpdatableProjectFields) (*api.UpdatableProjectFields, error) {
	pbUpdatableProjectFields := &api.UpdatableProjectFields{}
	if fields.Name != nil {
		pbUpdatableProjectFields.Name = &wrapperspb.StringValue{Value: *fields.Name}
	}
	if fields.AuthWebhookURL != nil {
		pbUpdatableProjectFields.AuthWebhookUrl = &wrapperspb.StringValue{Value: *fields.AuthWebhookURL}
	}
	if fields.AuthWebhookMethods != nil {
		pbUpdatableProjectFields.AuthWebhookMethods = &api.UpdatableProjectFields_AuthWebhookMethods{
			Methods: *fields.AuthWebhookMethods,
		}
	} else {
		pbUpdatableProjectFields.AuthWebhookMethods = nil
	}
	if fields.EventWebhookURL != nil {
		pbUpdatableProjectFields.EventWebhookUrl = &wrapperspb.StringValue{Value: *fields.EventWebhookURL}
	}
	if fields.EventWebhookEvents != nil {
		pbUpdatableProjectFields.EventWebhookEvents = &api.UpdatableProjectFields_EventWebhookEvents{
			Events: *fields.EventWebhookEvents,
		}
	} else {
		pbUpdatableProjectFields.EventWebhookEvents = nil
	}
	if fields.ClientDeactivateThreshold != nil {
		pbUpdatableProjectFields.ClientDeactivateThreshold = &wrapperspb.StringValue{
			Value: *fields.ClientDeactivateThreshold,
		}
	}
	if fields.MaxSubscribersPerDocument != nil {
		pbUpdatableProjectFields.MaxSubscribersPerDocument = &wrapperspb.Int32Value{
			Value: int32(*fields.MaxSubscribersPerDocument),
		}
	}
	if fields.MaxAttachmentsPerDocument != nil {
		pbUpdatableProjectFields.MaxAttachmentsPerDocument = &wrapperspb.Int32Value{
			Value: int32(*fields.MaxAttachmentsPerDocument),
		}
	}
	if fields.MaxSizePerDocument != nil {
		pbUpdatableProjectFields.MaxSizePerDocument = &wrapperspb.Int32Value{
			Value: int32(*fields.MaxSizePerDocument),
		}
	}
	return pbUpdatableProjectFields, nil
}

// ToRules converts the given model format to Protobuf format.
func ToRules(rules []types.Rule) []*api.Rule {
	var pbRules []*api.Rule
	for _, rule := range rules {
		pbRules = append(pbRules, &api.Rule{
			Type: rule.Type,
			Path: rule.Path,
		})
	}
	return pbRules
}

// ToSchema converts the given model format to Protobuf format.
func ToSchema(schema *types.Schema) *api.Schema {
	return &api.Schema{
		Name:      schema.Name,
		Version:   int32(schema.Version),
		Body:      schema.Body,
		Rules:     ToRules(schema.Rules),
		CreatedAt: timestamppb.New(schema.CreatedAt),
	}
}

// ToSchemas converts the given model format to Protobuf format.
func ToSchemas(schemas []*types.Schema) []*api.Schema {
	var pbSchemas []*api.Schema
	for _, schema := range schemas {
		pbSchemas = append(pbSchemas, ToSchema(schema))
	}
	return pbSchemas
}
