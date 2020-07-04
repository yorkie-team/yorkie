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
	"errors"

	"github.com/yorkie-team/yorkie/api"
	"github.com/yorkie-team/yorkie/pkg/document/change"
	"github.com/yorkie-team/yorkie/pkg/document/checkpoint"
	"github.com/yorkie-team/yorkie/pkg/document/json"
	"github.com/yorkie-team/yorkie/pkg/document/key"
	"github.com/yorkie-team/yorkie/pkg/document/operation"
	"github.com/yorkie-team/yorkie/pkg/document/time"
	"github.com/yorkie-team/yorkie/pkg/log"
	"github.com/yorkie-team/yorkie/pkg/types"
)

var (
	errPackRequired       = errors.New("pack required")
	errCheckpointRequired = errors.New("checkpoint required")
)

// FromChangePack converts the given Protobuf format to model format.
// TODO There is no guarantee that the message sent by the client is perfect.
//      We should check mandatory fields and change the interface with error return.
func FromChangePack(pbPack *api.ChangePack) (*change.Pack, error) {
	if pbPack == nil {
		log.Logger.Error(errPackRequired)
		return nil, errPackRequired
	}
	if pbPack.Checkpoint == nil {
		log.Logger.Error(errCheckpointRequired)
		return nil, errCheckpointRequired
	}

	return &change.Pack{
		DocumentKey:     fromDocumentKey(pbPack.DocumentKey),
		Checkpoint:      fromCheckpoint(pbPack.Checkpoint),
		Changes:         fromChanges(pbPack.Changes),
		Snapshot:        pbPack.Snapshot,
		MinSyncedTicket: fromTimeTicket(pbPack.MinSyncedTicket),
	}, nil
}

func fromDocumentKey(pbKey *api.DocumentKey) *key.Key {
	return &key.Key{
		Collection: pbKey.Collection,
		Document:   pbKey.Document,
	}
}

func fromCheckpoint(pbCheckpoint *api.Checkpoint) *checkpoint.Checkpoint {
	return checkpoint.New(
		pbCheckpoint.ServerSeq,
		pbCheckpoint.ClientSeq,
	)
}

func fromChanges(pbChanges []*api.Change) []*change.Change {
	var changes []*change.Change
	for _, pbChange := range pbChanges {
		changes = append(changes, change.New(
			fromChangeID(pbChange.Id),
			pbChange.Message,
			FromOperations(pbChange.Operations),
		))
	}

	return changes
}

func fromChangeID(id *api.ChangeID) *change.ID {
	return change.NewID(
		id.ClientSeq,
		id.Lamport,
		time.ActorIDFromHex(id.ActorId),
	)
}

// FromDocumentKeys converts the given Protobuf format to model format.
func FromDocumentKeys(pbKeys []*api.DocumentKey) []*key.Key {
	var keys []*key.Key
	for _, pbKey := range pbKeys {
		keys = append(keys, fromDocumentKey(pbKey))
	}
	return keys
}

func FromEventType(eventType api.EventType) types.EventType {
	switch eventType {
	case api.EventType_DOCUMENTS_CHANGED:
		return types.DocumentsChangeEvent
	case api.EventType_DOCUMENTS_WATCHED:
		return types.DocumentsWatchedEvent
	case api.EventType_DOCUMENTS_UNWATCHED:
		return types.DocumentsUnwatchedEvent
	default:
		panic("unsupported type")
	}
}

// FromOperations converts the given Protobuf format to model format.
func FromOperations(pbOps []*api.Operation) []operation.Operation {
	var ops []operation.Operation

	for _, pbOp := range pbOps {
		var op operation.Operation
		switch decoded := pbOp.Body.(type) {
		case *api.Operation_Set_:
			op = operation.NewSet(
				fromTimeTicket(decoded.Set.ParentCreatedAt),
				decoded.Set.Key,
				fromElement(decoded.Set.Value),
				fromTimeTicket(decoded.Set.ExecutedAt),
			)
		case *api.Operation_Add_:
			op = operation.NewAdd(
				fromTimeTicket(decoded.Add.ParentCreatedAt),
				fromTimeTicket(decoded.Add.PrevCreatedAt),
				fromElement(decoded.Add.Value),
				fromTimeTicket(decoded.Add.ExecutedAt),
			)
		case *api.Operation_Move_:
			op = operation.NewMove(
				fromTimeTicket(decoded.Move.ParentCreatedAt),
				fromTimeTicket(decoded.Move.PrevCreatedAt),
				fromTimeTicket(decoded.Move.CreatedAt),
				fromTimeTicket(decoded.Move.ExecutedAt),
			)
		case *api.Operation_Remove_:
			op = operation.NewRemove(
				fromTimeTicket(decoded.Remove.ParentCreatedAt),
				fromTimeTicket(decoded.Remove.CreatedAt),
				fromTimeTicket(decoded.Remove.ExecutedAt),
			)
		case *api.Operation_Edit_:
			op = operation.NewEdit(
				fromTimeTicket(decoded.Edit.ParentCreatedAt),
				fromTextNodePos(decoded.Edit.From),
				fromTextNodePos(decoded.Edit.To),
				fromCreatedAtMapByActor(decoded.Edit.CreatedAtMapByActor),
				decoded.Edit.Content,
				fromTimeTicket(decoded.Edit.ExecutedAt),
			)
		case *api.Operation_Select_:
			op = operation.NewSelect(
				fromTimeTicket(decoded.Select.ParentCreatedAt),
				fromTextNodePos(decoded.Select.From),
				fromTextNodePos(decoded.Select.To),
				fromTimeTicket(decoded.Select.ExecutedAt),
			)
		case *api.Operation_RichEdit_:
			op = operation.NewRichEdit(
				fromTimeTicket(decoded.RichEdit.ParentCreatedAt),
				fromTextNodePos(decoded.RichEdit.From),
				fromTextNodePos(decoded.RichEdit.To),
				fromCreatedAtMapByActor(decoded.RichEdit.CreatedAtMapByActor),
				decoded.RichEdit.Content,
				decoded.RichEdit.Attributes,
				fromTimeTicket(decoded.RichEdit.ExecutedAt),
			)
		case *api.Operation_Style_:
			op = operation.NewStyle(
				fromTimeTicket(decoded.Style.ParentCreatedAt),
				fromTextNodePos(decoded.Style.From),
				fromTextNodePos(decoded.Style.To),
				decoded.Style.Attributes,
				fromTimeTicket(decoded.Style.ExecutedAt),
			)
		default:
			panic("unsupported operation")
		}
		ops = append(ops, op)
	}

	return ops
}

func fromCreatedAtMapByActor(
	pbCreatedAtMapByActor map[string]*api.TimeTicket,
) map[string]*time.Ticket {
	createdAtMapByActor := make(map[string]*time.Ticket)
	for actor, pbTicket := range pbCreatedAtMapByActor {
		createdAtMapByActor[actor] = fromTimeTicket(pbTicket)
	}
	return createdAtMapByActor
}

func fromTextNodePos(pbPos *api.TextNodePos) *json.RGATreeSplitNodePos {
	return json.NewRGATreeSplitNodePos(
		json.NewRGATreeSplitNodeID(fromTimeTicket(pbPos.CreatedAt), int(pbPos.Offset)),
		int(pbPos.RelativeOffset),
	)
}

func fromTimeTicket(pbTicket *api.TimeTicket) *time.Ticket {
	if pbTicket == nil {
		return nil
	}

	return time.NewTicket(
		pbTicket.Lamport,
		pbTicket.Delimiter,
		time.ActorIDFromHex(pbTicket.ActorId),
	)
}

func fromElement(pbElement *api.JSONElementSimple) json.Element {
	switch pbType := pbElement.Type; pbType {
	case api.ValueType_JSON_OBJECT:
		return json.NewObject(
			json.NewRHTPriorityQueueMap(),
			fromTimeTicket(pbElement.CreatedAt),
		)
	case api.ValueType_JSON_ARRAY:
		return json.NewArray(
			json.NewRGATreeList(),
			fromTimeTicket(pbElement.CreatedAt),
		)
	case api.ValueType_BOOLEAN:
		fallthrough
	case api.ValueType_INTEGER:
		fallthrough
	case api.ValueType_LONG:
		fallthrough
	case api.ValueType_DOUBLE:
		fallthrough
	case api.ValueType_STRING:
		fallthrough
	case api.ValueType_BYTES:
		fallthrough
	case api.ValueType_DATE:
		return json.NewPrimitive(
			json.ValueFromBytes(fromValueType(pbType), pbElement.Value),
			fromTimeTicket(pbElement.CreatedAt),
		)
	case api.ValueType_TEXT:
		return json.NewText(
			json.NewRGATreeSplit(json.InitialTextNode()),
			fromTimeTicket(pbElement.CreatedAt),
		)
	case api.ValueType_RICH_TEXT:
		return json.NewInitialRichText(
			json.NewRGATreeSplit(json.InitialRichTextNode()),
			fromTimeTicket(pbElement.CreatedAt),
		)
	}

	panic("fail to decode element")
}

func fromValueType(valueType api.ValueType) json.ValueType {
	switch valueType {
	case api.ValueType_BOOLEAN:
		return json.Boolean
	case api.ValueType_INTEGER:
		return json.Integer
	case api.ValueType_LONG:
		return json.Long
	case api.ValueType_DOUBLE:
		return json.Double
	case api.ValueType_STRING:
		return json.String
	case api.ValueType_BYTES:
		return json.Bytes
	case api.ValueType_DATE:
		return json.Date
	}

	panic("fail to decode value type")
}
