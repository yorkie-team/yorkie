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

	protoTypes "github.com/gogo/protobuf/types"

	"github.com/yorkie-team/yorkie/api/types"
	api "github.com/yorkie-team/yorkie/api/yorkie/v1"
	"github.com/yorkie-team/yorkie/pkg/document/change"
	"github.com/yorkie-team/yorkie/pkg/document/crdt"
	"github.com/yorkie-team/yorkie/pkg/document/innerpresence"
	"github.com/yorkie-team/yorkie/pkg/document/operations"
	"github.com/yorkie-team/yorkie/pkg/document/time"
)

// ToUser converts the given model format to Protobuf format.
func ToUser(user *types.User) (*api.User, error) {
	pbCreatedAt, err := protoTypes.TimestampProto(user.CreatedAt)
	if err != nil {
		return nil, fmt.Errorf("convert createdAt to protobuf: %w", err)
	}

	return &api.User{
		Id:        user.ID.String(),
		Username:  user.Username,
		CreatedAt: pbCreatedAt,
	}, nil
}

// ToProjects converts the given model to Protobuf.
func ToProjects(projects []*types.Project) ([]*api.Project, error) {
	var pbProjects []*api.Project
	for _, project := range projects {
		pbProject, err := ToProject(project)
		if err != nil {
			return nil, err
		}
		pbProjects = append(pbProjects, pbProject)
	}

	return pbProjects, nil
}

// ToProject converts the given model to Protobuf.
func ToProject(project *types.Project) (*api.Project, error) {
	pbCreatedAt, err := protoTypes.TimestampProto(project.CreatedAt)
	if err != nil {
		return nil, fmt.Errorf("convert createdAt to protobuf: %w", err)
	}
	pbUpdatedAt, err := protoTypes.TimestampProto(project.UpdatedAt)
	if err != nil {
		return nil, fmt.Errorf("convert updatedAt to protobuf: %w", err)
	}

	return &api.Project{
		Id:                        project.ID.String(),
		Name:                      project.Name,
		AuthWebhookUrl:            project.AuthWebhookURL,
		AuthWebhookMethods:        project.AuthWebhookMethods,
		ClientDeactivateThreshold: project.ClientDeactivateThreshold,
		PublicKey:                 project.PublicKey,
		SecretKey:                 project.SecretKey,
		CreatedAt:                 pbCreatedAt,
		UpdatedAt:                 pbUpdatedAt,
	}, nil
}

// ToDocumentSummaries converts the given model to Protobuf.
func ToDocumentSummaries(summaries []*types.DocumentSummary) ([]*api.DocumentSummary, error) {
	var pbSummaries []*api.DocumentSummary
	for _, summary := range summaries {
		pbSummary, err := ToDocumentSummary(summary)
		if err != nil {
			return nil, err
		}
		pbSummaries = append(pbSummaries, pbSummary)
	}
	return pbSummaries, nil
}

// ToDocumentSummary converts the given model to Protobuf format.
func ToDocumentSummary(summary *types.DocumentSummary) (*api.DocumentSummary, error) {
	pbCreatedAt, err := protoTypes.TimestampProto(summary.CreatedAt)
	if err != nil {
		return nil, fmt.Errorf("convert createdAt to protobuf: %w", err)
	}
	pbAccessedAt, err := protoTypes.TimestampProto(summary.AccessedAt)
	if err != nil {
		return nil, fmt.Errorf("convert accessedAt to protobuf: %w", err)
	}
	pbUpdatedAt, err := protoTypes.TimestampProto(summary.UpdatedAt)
	if err != nil {
		return nil, fmt.Errorf("convert updatedAt to protobuf: %w", err)
	}

	return &api.DocumentSummary{
		Id:         summary.ID.String(),
		Key:        summary.Key.String(),
		CreatedAt:  pbCreatedAt,
		AccessedAt: pbAccessedAt,
		UpdatedAt:  pbUpdatedAt,
		Snapshot:   summary.Snapshot,
	}, nil
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
func ToPresenceChange(p *innerpresence.PresenceChange) *api.PresenceChange {
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

	return &api.ChangePack{
		DocumentKey:     pack.DocumentKey.String(),
		Checkpoint:      ToCheckpoint(pack.Checkpoint),
		Changes:         pbChanges,
		Snapshot:        pack.Snapshot,
		MinSyncedTicket: ToTimeTicket(pack.MinSyncedTicket),
		IsRemoved:       pack.IsRemoved,
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
func ToChangeID(id change.ID) *api.ChangeID {
	return &api.ChangeID{
		ClientSeq: id.ClientSeq(),
		ServerSeq: id.ServerSeq(),
		Lamport:   id.Lamport(),
		ActorId:   id.ActorID().Bytes(),
	}
}

// ToDocEventType converts the given model format to Protobuf format.
func ToDocEventType(eventType types.DocEventType) (api.DocEventType, error) {
	switch eventType {
	case types.DocumentChangedEvent:
		return api.DocEventType_DOC_EVENT_TYPE_DOCUMENT_CHANGED, nil
	case types.DocumentWatchedEvent:
		return api.DocEventType_DOC_EVENT_TYPE_DOCUMENT_WATCHED, nil
	case types.DocumentUnwatchedEvent:
		return api.DocEventType_DOC_EVENT_TYPE_DOCUMENT_UNWATCHED, nil
	default:
		return 0, fmt.Errorf("%s: %w", eventType, ErrUnsupportedEventType)
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

		pbChanges = append(pbChanges, &api.Change{
			Id:             ToChangeID(c.ID()),
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
			ParentCreatedAt:     ToTimeTicket(e.ParentCreatedAt()),
			From:                toTextNodePos(e.From()),
			To:                  toTextNodePos(e.To()),
			CreatedAtMapByActor: toCreatedAtMapByActor(e.CreatedAtMapByActor()),
			Content:             e.Content(),
			Attributes:          e.Attributes(),
			ExecutedAt:          ToTimeTicket(e.ExecutedAt()),
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
			ParentCreatedAt:     ToTimeTicket(e.ParentCreatedAt()),
			From:                toTreePos(e.FromPos()),
			To:                  toTreePos(e.ToPos()),
			CreatedAtMapByActor: toCreatedAtMapByActor(e.CreatedAtMapByActor()),
			Contents:            ToTreeNodesWhenEdit(e.Contents()),
			ExecutedAt:          ToTimeTicket(e.ExecutedAt()),
		},
	}, nil
}

func toTreeStyle(style *operations.TreeStyle) (*api.Operation_TreeStyle_, error) {
	return &api.Operation_TreeStyle_{
		TreeStyle: &api.Operation_TreeStyle{
			ParentCreatedAt: ToTimeTicket(style.ParentCreatedAt()),
			From:            toTreePos(style.FromPos()),
			To:              toTreePos(style.ToPos()),
			Attributes:      style.Attributes(),
			ExecutedAt:      ToTimeTicket(style.ExecutedAt()),
		},
	}, nil
}

func toJSONElementSimple(elem crdt.Element) (*api.JSONElementSimple, error) {
	switch elem := elem.(type) {
	case *crdt.Object:
		return &api.JSONElementSimple{
			Type:      api.ValueType_VALUE_TYPE_JSON_OBJECT,
			CreatedAt: ToTimeTicket(elem.CreatedAt()),
		}, nil
	case *crdt.Array:
		return &api.JSONElementSimple{
			Type:      api.ValueType_VALUE_TYPE_JSON_ARRAY,
			CreatedAt: ToTimeTicket(elem.CreatedAt()),
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

func toCreatedAtMapByActor(
	createdAtMapByActor map[string]*time.Ticket,
) map[string]*api.TimeTicket {
	pbCreatedAtMapByActor := make(map[string]*api.TimeTicket)
	for actor, createdAt := range createdAtMapByActor {
		pbCreatedAtMapByActor[actor] = ToTimeTicket(createdAt)
	}
	return pbCreatedAtMapByActor
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
		pbUpdatableProjectFields.Name = &protoTypes.StringValue{Value: *fields.Name}
	}
	if fields.AuthWebhookURL != nil {
		pbUpdatableProjectFields.AuthWebhookUrl = &protoTypes.StringValue{Value: *fields.AuthWebhookURL}
	}
	if fields.AuthWebhookMethods != nil {
		pbUpdatableProjectFields.AuthWebhookMethods = &api.UpdatableProjectFields_AuthWebhookMethods{
			Methods: *fields.AuthWebhookMethods,
		}
	} else {
		pbUpdatableProjectFields.AuthWebhookMethods = nil
	}
	if fields.ClientDeactivateThreshold != nil {
		pbUpdatableProjectFields.ClientDeactivateThreshold = &protoTypes.StringValue{
			Value: *fields.ClientDeactivateThreshold,
		}
	}
	return pbUpdatableProjectFields, nil
}
