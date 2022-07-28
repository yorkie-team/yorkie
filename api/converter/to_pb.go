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

	protoTypes "github.com/gogo/protobuf/types"

	"github.com/yorkie-team/yorkie/api/types"
	"github.com/yorkie-team/yorkie/pkg/document/change"
	"github.com/yorkie-team/yorkie/pkg/document/json"
	"github.com/yorkie-team/yorkie/pkg/document/key"
	"github.com/yorkie-team/yorkie/pkg/document/operations"
	"github.com/yorkie-team/yorkie/pkg/document/time"
	"github.com/yorkie-team/yorkie/server/backend/sync"
)

// ToProjects converts the given model to Protobuf.
func ToProjects(projects []*types.Project) ([]*v1.Project, error) {
	var pbProjects []*v1.Project
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
func ToProject(project *types.Project) (*v1.Project, error) {
	pbCreatedAt, err := protoTypes.TimestampProto(project.CreatedAt)
	if err != nil {
		return nil, err
	}
	pbUpdatedAt, err := protoTypes.TimestampProto(project.UpdatedAt)
	if err != nil {
		return nil, err
	}

	return &v1.Project{
		Id:                 project.ID.String(),
		Name:               project.Name,
		AuthWebhookUrl:     project.AuthWebhookURL,
		AuthWebhookMethods: project.AuthWebhookMethods,
		PublicKey:          project.PublicKey,
		SecretKey:          project.SecretKey,
		CreatedAt:          pbCreatedAt,
		UpdatedAt:          pbUpdatedAt,
	}, nil
}

// ToDocumentSummaries converts the given model to Protobuf.
func ToDocumentSummaries(summaries []*types.DocumentSummary) ([]*v1.DocumentSummary, error) {
	var pbSummaries []*v1.DocumentSummary
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
func ToDocumentSummary(summary *types.DocumentSummary) (*v1.DocumentSummary, error) {
	pbCreatedAt, err := protoTypes.TimestampProto(summary.CreatedAt)
	if err != nil {
		return nil, err
	}
	pbAccessedAt, err := protoTypes.TimestampProto(summary.AccessedAt)
	if err != nil {
		return nil, err
	}
	pbUpdatedAt, err := protoTypes.TimestampProto(summary.UpdatedAt)
	if err != nil {
		return nil, err
	}

	return &v1.DocumentSummary{
		Id:         summary.ID.String(),
		Key:        summary.Key.String(),
		CreatedAt:  pbCreatedAt,
		AccessedAt: pbAccessedAt,
		UpdatedAt:  pbUpdatedAt,
		Snapshot:   summary.Snapshot,
	}, nil
}

// ToDocumentSummaries converts the given model to Protobuf.
func ToDocumentSummaries(summaries []*types.DocumentSummary) ([]*v1.DocumentSummary, error) {
	var pbSummaries []*v1.DocumentSummary
	for _, summary := range summaries {
		pbSummary, err := ToDocumentSummary(summary)
		if err != nil {
			return nil, err
		}
		pbSummaries = append(pbSummaries, pbSummary)
	}
	return pbSummaries, nil
}

// ToClient converts the given model to Protobuf format.
func ToClient(client types.Client) *v1.Client {
	return &v1.Client{
		Id:       client.ID.Bytes(),
		Presence: ToPresenceInfo(client.PresenceInfo),
	}
}

// ToPresenceInfo converts the given model to Protobuf format.
func ToPresenceInfo(info types.PresenceInfo) *v1.Presence {
	return &v1.Presence{
		Clock: info.Clock,
		Data:  info.Presence,
	}
}

// ToChangePack converts the given model format to Protobuf format.
func ToChangePack(pack *change.Pack) (*v1.ChangePack, error) {
	pbChanges, err := ToChanges(pack.Changes)
	if err != nil {
		return nil, err
	}

	return &v1.ChangePack{
		DocumentKey:     pack.DocumentKey.String(),
		Checkpoint:      ToCheckpoint(pack.Checkpoint),
		Changes:         pbChanges,
		Snapshot:        pack.Snapshot,
		MinSyncedTicket: ToTimeTicket(pack.MinSyncedTicket),
	}, nil
}

// ToCheckpoint converts the given model format to Protobuf format.
func ToCheckpoint(cp change.Checkpoint) *v1.Checkpoint {
	return &v1.Checkpoint{
		ServerSeq: cp.ServerSeq,
		ClientSeq: cp.ClientSeq,
	}
}

// ToChangeID converts the given model format to Protobuf format.
func ToChangeID(id change.ID) *v1.ChangeID {
	return &v1.ChangeID{
		ClientSeq: id.ClientSeq(),
		ServerSeq: id.ServerSeq(),
		Lamport:   id.Lamport(),
		ActorId:   id.ActorID().Bytes(),
	}
}

// ToDocumentKeys converts the given model format to Protobuf format.
func ToDocumentKeys(keys []key.Key) []string {
	var keyList []string
	for _, k := range keys {
		keyList = append(keyList, k.String())
	}
	return keyList
}

// ToClientsMap converts the given model to Protobuf format.
func ToClientsMap(clientsMap map[string][]types.Client) map[string]*v1.Clients {
	pbClientsMap := make(map[string]*v1.Clients)

	for k, clients := range clientsMap {
		var pbClients []*v1.Client
		for _, client := range clients {
			pbClients = append(pbClients, ToClient(client))
		}

		pbClientsMap[k] = &v1.Clients{
			Clients: pbClients,
		}
	}

	return pbClientsMap
}

// ToDocEventType converts the given model format to Protobuf format.
func ToDocEventType(eventType types.DocEventType) (v1.DocEventType, error) {
	switch eventType {
	case types.DocumentsChangedEvent:
		return v1.DocEventType_DOCUMENTS_CHANGED, nil
	case types.DocumentsWatchedEvent:
		return v1.DocEventType_DOCUMENTS_WATCHED, nil
	case types.DocumentsUnwatchedEvent:
		return v1.DocEventType_DOCUMENTS_UNWATCHED, nil
	case types.PresenceChangedEvent:
		return v1.DocEventType_PRESENCE_CHANGED, nil
	default:
		return 0, fmt.Errorf("%s: %w", eventType, ErrUnsupportedEventType)
	}
}

// ToDocEvent converts the given model to Protobuf format.
func ToDocEvent(docEvent sync.DocEvent) (*v1.DocEvent, error) {
	eventType, err := ToDocEventType(docEvent.Type)
	if err != nil {
		return nil, err
	}

	return &v1.DocEvent{
		Type:         eventType,
		Publisher:    ToClient(docEvent.Publisher),
		DocumentKeys: ToDocumentKeys(docEvent.DocumentKeys),
	}, nil
}

// ToOperations converts the given model format to Protobuf format.
func ToOperations(ops []operations.Operation) ([]*v1.Operation, error) {
	var pbOperations []*v1.Operation

	for _, o := range ops {
		pbOperation := &v1.Operation{}
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
		case *operations.Select:
			pbOperation.Body, err = toSelect(op)
		case *operations.RichEdit:
			pbOperation.Body, err = toRichEdit(op)
		case *operations.Style:
			pbOperation.Body, err = toStyle(op)
		case *operations.Increase:
			pbOperation.Body, err = toIncrease(op)
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
func ToTimeTicket(ticket *time.Ticket) *v1.TimeTicket {
	if ticket == nil {
		return nil
	}

	return &v1.TimeTicket{
		Lamport:   ticket.Lamport(),
		Delimiter: ticket.Delimiter(),
		ActorId:   ticket.ActorIDBytes(),
	}
}

// ToChanges converts the given model format to Protobuf format.
func ToChanges(changes []*change.Change) ([]*v1.Change, error) {
	var pbChanges []*v1.Change

	for _, c := range changes {
		pbOperations, err := ToOperations(c.Operations())
		if err != nil {
			return nil, err
		}

		pbChanges = append(pbChanges, &v1.Change{
			Id:         ToChangeID(c.ID()),
			Message:    c.Message(),
			Operations: pbOperations,
		})
	}

	return pbChanges, nil
}

func toSet(set *operations.Set) (*v1.Operation_Set_, error) {
	pbElem, err := toJSONElementSimple(set.Value())
	if err != nil {
		return nil, err
	}

	return &v1.Operation_Set_{
		Set: &v1.Operation_Set{
			ParentCreatedAt: ToTimeTicket(set.ParentCreatedAt()),
			Key:             set.Key(),
			Value:           pbElem,
			ExecutedAt:      ToTimeTicket(set.ExecutedAt()),
		},
	}, nil
}

func toAdd(add *operations.Add) (*v1.Operation_Add_, error) {
	pbElem, err := toJSONElementSimple(add.Value())
	if err != nil {
		return nil, err
	}

	return &v1.Operation_Add_{
		Add: &v1.Operation_Add{
			ParentCreatedAt: ToTimeTicket(add.ParentCreatedAt()),
			PrevCreatedAt:   ToTimeTicket(add.PrevCreatedAt()),
			Value:           pbElem,
			ExecutedAt:      ToTimeTicket(add.ExecutedAt()),
		},
	}, nil
}

func toMove(move *operations.Move) (*v1.Operation_Move_, error) {
	return &v1.Operation_Move_{
		Move: &v1.Operation_Move{
			ParentCreatedAt: ToTimeTicket(move.ParentCreatedAt()),
			PrevCreatedAt:   ToTimeTicket(move.PrevCreatedAt()),
			CreatedAt:       ToTimeTicket(move.CreatedAt()),
			ExecutedAt:      ToTimeTicket(move.ExecutedAt()),
		},
	}, nil
}

func toRemove(remove *operations.Remove) (*v1.Operation_Remove_, error) {
	return &v1.Operation_Remove_{
		Remove: &v1.Operation_Remove{
			ParentCreatedAt: ToTimeTicket(remove.ParentCreatedAt()),
			CreatedAt:       ToTimeTicket(remove.CreatedAt()),
			ExecutedAt:      ToTimeTicket(remove.ExecutedAt()),
		},
	}, nil
}

func toEdit(edit *operations.Edit) (*v1.Operation_Edit_, error) {
	return &v1.Operation_Edit_{
		Edit: &v1.Operation_Edit{
			ParentCreatedAt:     ToTimeTicket(edit.ParentCreatedAt()),
			From:                toTextNodePos(edit.From()),
			To:                  toTextNodePos(edit.To()),
			CreatedAtMapByActor: toCreatedAtMapByActor(edit.CreatedAtMapByActor()),
			Content:             edit.Content(),
			ExecutedAt:          ToTimeTicket(edit.ExecutedAt()),
		},
	}, nil
}

func toSelect(s *operations.Select) (*v1.Operation_Select_, error) {
	return &v1.Operation_Select_{
		Select: &v1.Operation_Select{
			ParentCreatedAt: ToTimeTicket(s.ParentCreatedAt()),
			From:            toTextNodePos(s.From()),
			To:              toTextNodePos(s.To()),
			ExecutedAt:      ToTimeTicket(s.ExecutedAt()),
		},
	}, nil
}

func toRichEdit(richEdit *operations.RichEdit) (*v1.Operation_RichEdit_, error) {
	return &v1.Operation_RichEdit_{
		RichEdit: &v1.Operation_RichEdit{
			ParentCreatedAt:     ToTimeTicket(richEdit.ParentCreatedAt()),
			From:                toTextNodePos(richEdit.From()),
			To:                  toTextNodePos(richEdit.To()),
			CreatedAtMapByActor: toCreatedAtMapByActor(richEdit.CreatedAtMapByActor()),
			Content:             richEdit.Content(),
			Attributes:          richEdit.Attributes(),
			ExecutedAt:          ToTimeTicket(richEdit.ExecutedAt()),
		},
	}, nil
}

func toStyle(style *operations.Style) (*v1.Operation_Style_, error) {
	return &v1.Operation_Style_{
		Style: &v1.Operation_Style{
			ParentCreatedAt: ToTimeTicket(style.ParentCreatedAt()),
			From:            toTextNodePos(style.From()),
			To:              toTextNodePos(style.To()),
			Attributes:      style.Attributes(),
			ExecutedAt:      ToTimeTicket(style.ExecutedAt()),
		},
	}, nil
}

func toIncrease(increase *operations.Increase) (*v1.Operation_Increase_, error) {
	pbElem, err := toJSONElementSimple(increase.Value())
	if err != nil {
		return nil, err
	}

	return &v1.Operation_Increase_{
		Increase: &v1.Operation_Increase{
			ParentCreatedAt: ToTimeTicket(increase.ParentCreatedAt()),
			Value:           pbElem,
			ExecutedAt:      ToTimeTicket(increase.ExecutedAt()),
		},
	}, nil
}

func toJSONElementSimple(elem json.Element) (*v1.JSONElementSimple, error) {
	switch elem := elem.(type) {
	case *json.Object:
		return &v1.JSONElementSimple{
			Type:      v1.ValueType_JSON_OBJECT,
			CreatedAt: ToTimeTicket(elem.CreatedAt()),
		}, nil
	case *json.Array:
		return &v1.JSONElementSimple{
			Type:      v1.ValueType_JSON_ARRAY,
			CreatedAt: ToTimeTicket(elem.CreatedAt()),
		}, nil
	case *json.Primitive:
		pbValueType, err := toValueType(elem.ValueType())
		if err != nil {
			return nil, err
		}

		return &v1.JSONElementSimple{
			Type:      pbValueType,
			CreatedAt: ToTimeTicket(elem.CreatedAt()),
			Value:     elem.Bytes(),
		}, nil
	case *json.Text:
		return &v1.JSONElementSimple{
			Type:      v1.ValueType_TEXT,
			CreatedAt: ToTimeTicket(elem.CreatedAt()),
		}, nil
	case *json.RichText:
		return &v1.JSONElementSimple{
			Type:      v1.ValueType_RICH_TEXT,
			CreatedAt: ToTimeTicket(elem.CreatedAt()),
		}, nil
	case *json.Counter:
		pbCounterType, err := toCounterType(elem.ValueType())
		if err != nil {
			return nil, err
		}

		return &v1.JSONElementSimple{
			Type:      pbCounterType,
			CreatedAt: ToTimeTicket(elem.CreatedAt()),
			Value:     elem.Bytes(),
		}, nil
	}

	return nil, fmt.Errorf("%v, %w", reflect.TypeOf(elem), ErrUnsupportedElement)
}

func toTextNodePos(pos *json.RGATreeSplitNodePos) *v1.TextNodePos {
	return &v1.TextNodePos{
		CreatedAt:      ToTimeTicket(pos.ID().CreatedAt()),
		Offset:         int32(pos.ID().Offset()),
		RelativeOffset: int32(pos.RelativeOffset()),
	}
}

func toCreatedAtMapByActor(
	createdAtMapByActor map[string]*time.Ticket,
) map[string]*v1.TimeTicket {
	pbCreatedAtMapByActor := make(map[string]*v1.TimeTicket)
	for actor, createdAt := range createdAtMapByActor {
		pbCreatedAtMapByActor[actor] = ToTimeTicket(createdAt)
	}
	return pbCreatedAtMapByActor
}

func toValueType(valueType json.ValueType) (v1.ValueType, error) {
	switch valueType {
	case json.Null:
		return v1.ValueType_NULL, nil
	case json.Boolean:
		return v1.ValueType_BOOLEAN, nil
	case json.Integer:
		return v1.ValueType_INTEGER, nil
	case json.Long:
		return v1.ValueType_LONG, nil
	case json.Double:
		return v1.ValueType_DOUBLE, nil
	case json.String:
		return v1.ValueType_STRING, nil
	case json.Bytes:
		return v1.ValueType_BYTES, nil
	case json.Date:
		return v1.ValueType_DATE, nil
	}

	return 0, fmt.Errorf("%d, %w", valueType, ErrUnsupportedValueType)
}

func toCounterType(valueType json.CounterType) (v1.ValueType, error) {
	switch valueType {
	case json.IntegerCnt:
		return v1.ValueType_INTEGER_CNT, nil
	case json.LongCnt:
		return v1.ValueType_LONG_CNT, nil
	case json.DoubleCnt:
		return v1.ValueType_DOUBLE_CNT, nil
	}

	return 0, fmt.Errorf("%d, %w", valueType, ErrUnsupportedCounterType)
}

// ToUpdatableProjectFields converts the given model format to Protobuf format.
func ToUpdatableProjectFields(fields *types.UpdatableProjectFields) (*v1.UpdatableProjectFields, error) {
	pbUpdatableProjectFields := &v1.UpdatableProjectFields{}
	if fields.Name != nil {
		pbUpdatableProjectFields.Name = &protoTypes.StringValue{Value: *fields.Name}
	}
	if fields.AuthWebhookURL != nil {
		pbUpdatableProjectFields.AuthWebhookUrl = &protoTypes.StringValue{Value: *fields.AuthWebhookURL}
	}
	if fields.AuthWebhookMethods != nil {
		pbUpdatableProjectFields.AuthWebhookMethods = &v1.UpdatableProjectFields_AuthWebhookMethods{
			Methods: *fields.AuthWebhookMethods,
		}
	} else {
		pbUpdatableProjectFields.AuthWebhookMethods = nil
	}
	return pbUpdatableProjectFields, nil
}
