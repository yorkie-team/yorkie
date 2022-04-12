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

	"github.com/yorkie-team/yorkie/api"
	"github.com/yorkie-team/yorkie/pkg/document/change"
	"github.com/yorkie-team/yorkie/pkg/document/json"
	"github.com/yorkie-team/yorkie/pkg/document/key"
	"github.com/yorkie-team/yorkie/pkg/document/operations"
	"github.com/yorkie-team/yorkie/pkg/document/time"
	"github.com/yorkie-team/yorkie/pkg/types"
	"github.com/yorkie-team/yorkie/yorkie/backend/sync"
)

// ToDocumentSummaries converts the given model to Protobuf.
func ToDocumentSummaries(summaries []*types.DocumentSummary) []*api.DocumentSummary {
	pbSummaries := make([]*api.DocumentSummary, 0, len(summaries))
	for _, summary := range summaries {
		pbSummaries = append(pbSummaries, &api.DocumentSummary{
			Id:       summary.ID,
			Key:      ToDocumentKey(summary.Key),
			Snapshot: summary.Snapshot,
		})
	}
	return pbSummaries
}

// ToClient converts the given model to Protobuf format.
func ToClient(client types.Client) *api.Client {
	return &api.Client{
		Id:       client.ID.Bytes(),
		Metadata: ToMetadataInfo(client.MetadataInfo),
	}
}

// ToMetadataInfo converts the given model to Protobuf format.
func ToMetadataInfo(metadata types.MetadataInfo) *api.Metadata {
	return &api.Metadata{
		Clock: metadata.Clock,
		Data:  metadata.Data,
	}
}

// ToChangePack converts the given model format to Protobuf format.
func ToChangePack(pack *change.Pack) (*api.ChangePack, error) {
	pbChanges, err := ToChanges(pack.Changes)
	if err != nil {
		return nil, err
	}

	return &api.ChangePack{
		DocumentKey:     ToDocumentKey(pack.DocumentKey),
		Checkpoint:      ToCheckpoint(pack.Checkpoint),
		Changes:         pbChanges,
		Snapshot:        pack.Snapshot,
		MinSyncedTicket: ToTimeTicket(pack.MinSyncedTicket),
	}, nil
}

// ToDocumentKey converts the given model format to Protobuf format.
func ToDocumentKey(key key.Key) *api.DocumentKey {
	return &api.DocumentKey{
		Collection: key.Collection,
		Document:   key.Document,
	}
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

// ToDocumentKeys converts the given model format to Protobuf format.
func ToDocumentKeys(keys []key.Key) []*api.DocumentKey {
	var pbKeys []*api.DocumentKey
	for _, k := range keys {
		pbKeys = append(pbKeys, ToDocumentKey(k))
	}
	return pbKeys
}

// ToClientsMap converts the given model to Protobuf format.
func ToClientsMap(clientsMap map[string][]types.Client) map[string]*api.Clients {
	pbClientsMap := make(map[string]*api.Clients)

	for k, clients := range clientsMap {
		var pbClients []*api.Client
		for _, client := range clients {
			pbClients = append(pbClients, ToClient(client))
		}

		pbClientsMap[k] = &api.Clients{
			Clients: pbClients,
		}
	}

	return pbClientsMap
}

// ToDocEventType converts the given model format to Protobuf format.
func ToDocEventType(eventType types.DocEventType) (api.DocEventType, error) {
	switch eventType {
	case types.DocumentsChangedEvent:
		return api.DocEventType_DOCUMENTS_CHANGED, nil
	case types.DocumentsWatchedEvent:
		return api.DocEventType_DOCUMENTS_WATCHED, nil
	case types.DocumentsUnwatchedEvent:
		return api.DocEventType_DOCUMENTS_UNWATCHED, nil
	case types.MetadataChangedEvent:
		return api.DocEventType_METADATA_CHANGED, nil
	default:
		return 0, fmt.Errorf("%s: %w", eventType, ErrUnsupportedEventType)
	}
}

// ToDocEvent converts the given model to Protobuf format.
func ToDocEvent(docEvent sync.DocEvent) (*api.DocEvent, error) {
	eventType, err := ToDocEventType(docEvent.Type)
	if err != nil {
		return nil, err
	}

	return &api.DocEvent{
		Type:         eventType,
		Publisher:    ToClient(docEvent.Publisher),
		DocumentKeys: ToDocumentKeys(docEvent.DocumentKeys),
	}, nil
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
			Id:         ToChangeID(c.ID()),
			Message:    c.Message(),
			Operations: pbOperations,
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

func toEdit(edit *operations.Edit) (*api.Operation_Edit_, error) {
	return &api.Operation_Edit_{
		Edit: &api.Operation_Edit{
			ParentCreatedAt:     ToTimeTicket(edit.ParentCreatedAt()),
			From:                toTextNodePos(edit.From()),
			To:                  toTextNodePos(edit.To()),
			CreatedAtMapByActor: toCreatedAtMapByActor(edit.CreatedAtMapByActor()),
			Content:             edit.Content(),
			ExecutedAt:          ToTimeTicket(edit.ExecutedAt()),
		},
	}, nil
}

func toSelect(s *operations.Select) (*api.Operation_Select_, error) {
	return &api.Operation_Select_{
		Select: &api.Operation_Select{
			ParentCreatedAt: ToTimeTicket(s.ParentCreatedAt()),
			From:            toTextNodePos(s.From()),
			To:              toTextNodePos(s.To()),
			ExecutedAt:      ToTimeTicket(s.ExecutedAt()),
		},
	}, nil
}

func toRichEdit(richEdit *operations.RichEdit) (*api.Operation_RichEdit_, error) {
	return &api.Operation_RichEdit_{
		RichEdit: &api.Operation_RichEdit{
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

func toJSONElementSimple(elem json.Element) (*api.JSONElementSimple, error) {
	switch elem := elem.(type) {
	case *json.Object:
		return &api.JSONElementSimple{
			Type:      api.ValueType_JSON_OBJECT,
			CreatedAt: ToTimeTicket(elem.CreatedAt()),
		}, nil
	case *json.Array:
		return &api.JSONElementSimple{
			Type:      api.ValueType_JSON_ARRAY,
			CreatedAt: ToTimeTicket(elem.CreatedAt()),
		}, nil
	case *json.Primitive:
		pbValueType, err := toValueType(elem.ValueType())
		if err != nil {
			return nil, err
		}

		return &api.JSONElementSimple{
			Type:      pbValueType,
			CreatedAt: ToTimeTicket(elem.CreatedAt()),
			Value:     elem.Bytes(),
		}, nil
	case *json.Text:
		return &api.JSONElementSimple{
			Type:      api.ValueType_TEXT,
			CreatedAt: ToTimeTicket(elem.CreatedAt()),
		}, nil
	case *json.RichText:
		return &api.JSONElementSimple{
			Type:      api.ValueType_RICH_TEXT,
			CreatedAt: ToTimeTicket(elem.CreatedAt()),
		}, nil
	case *json.Counter:
		pbCounterType, err := toCounterType(elem.ValueType())
		if err != nil {
			return nil, err
		}

		return &api.JSONElementSimple{
			Type:      pbCounterType,
			CreatedAt: ToTimeTicket(elem.CreatedAt()),
			Value:     elem.Bytes(),
		}, nil
	}

	return nil, fmt.Errorf("%v, %w", reflect.TypeOf(elem), ErrUnsupportedElement)
}

func toTextNodePos(pos *json.RGATreeSplitNodePos) *api.TextNodePos {
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

func toValueType(valueType json.ValueType) (api.ValueType, error) {
	switch valueType {
	case json.Null:
		return api.ValueType_NULL, nil
	case json.Boolean:
		return api.ValueType_BOOLEAN, nil
	case json.Integer:
		return api.ValueType_INTEGER, nil
	case json.Long:
		return api.ValueType_LONG, nil
	case json.Double:
		return api.ValueType_DOUBLE, nil
	case json.String:
		return api.ValueType_STRING, nil
	case json.Bytes:
		return api.ValueType_BYTES, nil
	case json.Date:
		return api.ValueType_DATE, nil
	}

	return 0, fmt.Errorf("%d, %w", valueType, ErrUnsupportedValueType)
}

func toCounterType(valueType json.CounterType) (api.ValueType, error) {
	switch valueType {
	case json.IntegerCnt:
		return api.ValueType_INTEGER_CNT, nil
	case json.LongCnt:
		return api.ValueType_LONG_CNT, nil
	case json.DoubleCnt:
		return api.ValueType_DOUBLE_CNT, nil
	}

	return 0, fmt.Errorf("%d, %w", valueType, ErrUnsupportedCounterType)
}
