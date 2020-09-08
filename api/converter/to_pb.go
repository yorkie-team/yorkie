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
	"github.com/yorkie-team/yorkie/api"
	"github.com/yorkie-team/yorkie/pkg/document/change"
	"github.com/yorkie-team/yorkie/pkg/document/checkpoint"
	"github.com/yorkie-team/yorkie/pkg/document/json"
	"github.com/yorkie-team/yorkie/pkg/document/key"
	"github.com/yorkie-team/yorkie/pkg/document/operation"
	"github.com/yorkie-team/yorkie/pkg/document/time"
	"github.com/yorkie-team/yorkie/pkg/types"
)

// ToChangePack converts the given model format to Protobuf format.
func ToChangePack(pack *change.Pack) *api.ChangePack {
	return &api.ChangePack{
		DocumentKey:     toDocumentKey(pack.DocumentKey),
		Checkpoint:      toCheckpoint(pack.Checkpoint),
		Changes:         toChanges(pack.Changes),
		Snapshot:        pack.Snapshot,
		MinSyncedTicket: toTimeTicket(pack.MinSyncedTicket),
	}
}

func toDocumentKey(key *key.Key) *api.DocumentKey {
	return &api.DocumentKey{
		Collection: key.Collection,
		Document:   key.Document,
	}
}

func toCheckpoint(cp *checkpoint.Checkpoint) *api.Checkpoint {
	return &api.Checkpoint{
		ServerSeq: cp.ServerSeq,
		ClientSeq: cp.ClientSeq,
	}
}

func toChanges(changes []*change.Change) []*api.Change {
	var pbChanges []*api.Change
	for _, c := range changes {
		pbChanges = append(pbChanges, &api.Change{
			Id:         toChangeID(c.ID()),
			Message:    c.Message(),
			Operations: ToOperations(c.Operations()),
		})
	}

	return pbChanges
}

func toChangeID(id *change.ID) *api.ChangeID {
	return &api.ChangeID{
		ClientSeq: id.ClientSeq(),
		Lamport:   id.Lamport(),
		ActorId:   id.Actor().String(),
	}
}

// ToDocumentKeys converts the given model format to Protobuf format.
func ToDocumentKeys(keys ...*key.Key) []*api.DocumentKey {
	var pbKeys []*api.DocumentKey
	for _, k := range keys {
		pbKeys = append(pbKeys, toDocumentKey(k))
	}
	return pbKeys
}

func ToClientsMap(clientsMap map[string][]string) map[string]*api.Clients {
	pbClientsMap := make(map[string]*api.Clients)

	for k, clients := range clientsMap {
		var clientIds []string
		for _, client := range clients {
			clientIds = append(clientIds, client)
		}

		pbClientsMap[k] = &api.Clients{
			ClientIds: clientIds,
		}
	}

	return pbClientsMap
}

// ToEventType converts the given model format to Protobuf format.
func ToEventType(eventType types.EventType) api.EventType {
	switch eventType {
	case types.DocumentsChangeEvent:
		return api.EventType_DOCUMENTS_CHANGED
	case types.DocumentsWatchedEvent:
		return api.EventType_DOCUMENTS_WATCHED
	case types.DocumentsUnwatchedEvent:
		return api.EventType_DOCUMENTS_UNWATCHED
	default:
		panic("unsupported event")
	}
}

// ToOperations converts the given model format to Protobuf format.
func ToOperations(operations []operation.Operation) []*api.Operation {
	var pbOperations []*api.Operation

	for _, o := range operations {
		pbOperation := &api.Operation{}
		switch op := o.(type) {
		case *operation.Set:
			pbOperation.Body = &api.Operation_Set_{
				Set: &api.Operation_Set{
					ParentCreatedAt: toTimeTicket(op.ParentCreatedAt()),
					Key:             op.Key(),
					Value:           toJSONElementSimple(op.Value()),
					ExecutedAt:      toTimeTicket(op.ExecutedAt()),
				},
			}
		case *operation.Add:
			pbOperation.Body = &api.Operation_Add_{
				Add: &api.Operation_Add{
					ParentCreatedAt: toTimeTicket(op.ParentCreatedAt()),
					PrevCreatedAt:   toTimeTicket(op.PrevCreatedAt()),
					Value:           toJSONElementSimple(op.Value()),
					ExecutedAt:      toTimeTicket(op.ExecutedAt()),
				},
			}
		case *operation.Move:
			pbOperation.Body = &api.Operation_Move_{
				Move: &api.Operation_Move{
					ParentCreatedAt: toTimeTicket(op.ParentCreatedAt()),
					PrevCreatedAt:   toTimeTicket(op.PrevCreatedAt()),
					CreatedAt:       toTimeTicket(op.CreatedAt()),
					ExecutedAt:      toTimeTicket(op.ExecutedAt()),
				},
			}
		case *operation.Remove:
			pbOperation.Body = &api.Operation_Remove_{
				Remove: &api.Operation_Remove{
					ParentCreatedAt: toTimeTicket(op.ParentCreatedAt()),
					CreatedAt:       toTimeTicket(op.CreatedAt()),
					ExecutedAt:      toTimeTicket(op.ExecutedAt()),
				},
			}
		case *operation.Edit:
			pbOperation.Body = &api.Operation_Edit_{
				Edit: &api.Operation_Edit{
					ParentCreatedAt:     toTimeTicket(op.ParentCreatedAt()),
					From:                toTextNodePos(op.From()),
					To:                  toTextNodePos(op.To()),
					CreatedAtMapByActor: toCreatedAtMapByActor(op.CreatedAtMapByActor()),
					Content:             op.Content(),
					ExecutedAt:          toTimeTicket(op.ExecutedAt()),
				},
			}
		case *operation.Select:
			pbOperation.Body = &api.Operation_Select_{
				Select: &api.Operation_Select{
					ParentCreatedAt: toTimeTicket(op.ParentCreatedAt()),
					From:            toTextNodePos(op.From()),
					To:              toTextNodePos(op.To()),
					ExecutedAt:      toTimeTicket(op.ExecutedAt()),
				},
			}
		case *operation.RichEdit:
			pbOperation.Body = &api.Operation_RichEdit_{
				RichEdit: &api.Operation_RichEdit{
					ParentCreatedAt:     toTimeTicket(op.ParentCreatedAt()),
					From:                toTextNodePos(op.From()),
					To:                  toTextNodePos(op.To()),
					CreatedAtMapByActor: toCreatedAtMapByActor(op.CreatedAtMapByActor()),
					Content:             op.Content(),
					Attributes:          op.Attributes(),
					ExecutedAt:          toTimeTicket(op.ExecutedAt()),
				},
			}
		case *operation.Style:
			pbOperation.Body = &api.Operation_Style_{
				Style: &api.Operation_Style{
					ParentCreatedAt: toTimeTicket(op.ParentCreatedAt()),
					From:            toTextNodePos(op.From()),
					To:              toTextNodePos(op.To()),
					Attributes:      op.Attributes(),
					ExecutedAt:      toTimeTicket(op.ExecutedAt()),
				},
			}
		case *operation.Increase:
			pbOperation.Body = &api.Operation_Increase_{
				Increase: &api.Operation_Increase{
					ParentCreatedAt: toTimeTicket(op.ParentCreatedAt()),
					Value:           toJSONElementSimple(op.Value()),
					ExecutedAt:      toTimeTicket(op.ExecutedAt()),
				},
			}
		default:
			panic("unsupported operation")
		}
		pbOperations = append(pbOperations, pbOperation)
	}

	return pbOperations
}

func toJSONElementSimple(elem json.Element) *api.JSONElementSimple {
	switch elem := elem.(type) {
	case *json.Object:
		return &api.JSONElementSimple{
			Type:      api.ValueType_JSON_OBJECT,
			CreatedAt: toTimeTicket(elem.CreatedAt()),
		}
	case *json.Array:
		return &api.JSONElementSimple{
			Type:      api.ValueType_JSON_ARRAY,
			CreatedAt: toTimeTicket(elem.CreatedAt()),
		}
	case *json.Primitive:
		return &api.JSONElementSimple{
			Type:      toValueType(elem.ValueType()),
			CreatedAt: toTimeTicket(elem.CreatedAt()),
			Value:     elem.Bytes(),
		}
	case *json.Text:
		return &api.JSONElementSimple{
			Type:      api.ValueType_TEXT,
			CreatedAt: toTimeTicket(elem.CreatedAt()),
		}
	case *json.RichText:
		return &api.JSONElementSimple{
			Type:      api.ValueType_RICH_TEXT,
			CreatedAt: toTimeTicket(elem.CreatedAt()),
		}
	case *json.Counter:
		return &api.JSONElementSimple{
			Type:      toCounterType(elem.ValueType()),
			CreatedAt: toTimeTicket(elem.CreatedAt()),
			Value:     elem.Bytes(),
		}
	}
	panic("fail to encode JSONElement to protobuf")
}

func toTextNodePos(pos *json.RGATreeSplitNodePos) *api.TextNodePos {
	return &api.TextNodePos{
		CreatedAt:      toTimeTicket(pos.ID().CreatedAt()),
		Offset:         int32(pos.ID().Offset()),
		RelativeOffset: int32(pos.RelativeOffset()),
	}
}

func toCreatedAtMapByActor(
	createdAtMapByActor map[string]*time.Ticket,
) map[string]*api.TimeTicket {
	pbCreatedAtMapByActor := make(map[string]*api.TimeTicket)
	for actor, createdAt := range createdAtMapByActor {
		pbCreatedAtMapByActor[actor] = toTimeTicket(createdAt)
	}
	return pbCreatedAtMapByActor
}

func toTimeTicket(ticket *time.Ticket) *api.TimeTicket {
	if ticket == nil {
		return nil
	}

	return &api.TimeTicket{
		Lamport:   ticket.Lamport(),
		Delimiter: ticket.Delimiter(),
		ActorId:   ticket.ActorIDHex(),
	}
}

func toValueType(valueType json.ValueType) api.ValueType {
	switch valueType {
	case json.Boolean:
		return api.ValueType_BOOLEAN
	case json.Integer:
		return api.ValueType_INTEGER
	case json.Long:
		return api.ValueType_LONG
	case json.Double:
		return api.ValueType_DOUBLE
	case json.String:
		return api.ValueType_STRING
	case json.Bytes:
		return api.ValueType_BYTES
	case json.Date:
		return api.ValueType_DATE
	}

	panic("unsupported value type")
}

func toCounterType(valueType json.CounterType) api.ValueType {
	switch valueType {
	case json.IntegerCnt:
		return api.ValueType_INTEGER_CNT
	case json.LongCnt:
		return api.ValueType_LONG_CNT
	case json.DoubleCnt:
		return api.ValueType_DOUBLE_CNT
	}

	panic("unsupported value type")
}
