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
	"github.com/pkg/errors"

	"github.com/yorkie-team/yorkie/api"
	"github.com/yorkie-team/yorkie/pkg/document/change"
	"github.com/yorkie-team/yorkie/pkg/document/checkpoint"
	"github.com/yorkie-team/yorkie/pkg/document/json"
	"github.com/yorkie-team/yorkie/pkg/document/key"
	"github.com/yorkie-team/yorkie/pkg/document/operation"
	"github.com/yorkie-team/yorkie/pkg/document/time"
	"github.com/yorkie-team/yorkie/pkg/types"
	"github.com/yorkie-team/yorkie/yorkie/backend/sync"
)

// FromClient converts the given Protobuf format to model format.
func FromClient(pbClient *api.Client) (*types.Client, error) {
	id, err := time.ActorIDFromBytes(pbClient.Id)
	if err != nil {
		return nil, err
	}

	return &types.Client{
		ID:       id,
		Metadata: pbClient.Metadata,
	}, nil
}

// FromChangePack converts the given Protobuf format to model format.
func FromChangePack(pbPack *api.ChangePack) (*change.Pack, error) {
	if pbPack == nil {
		return nil, errors.WithStack(ErrPackRequired)
	}
	if pbPack.Checkpoint == nil {
		return nil, errors.WithStack(ErrCheckpointRequired)
	}

	changes, err := fromChanges(pbPack.Changes)
	if err != nil {
		return nil, err
	}

	minSyncedTicket, err := fromTimeTicket(pbPack.MinSyncedTicket)
	if err != nil {
		return nil, err
	}

	return &change.Pack{
		DocumentKey:     fromDocumentKey(pbPack.DocumentKey),
		Checkpoint:      fromCheckpoint(pbPack.Checkpoint),
		Changes:         changes,
		Snapshot:        pbPack.Snapshot,
		MinSyncedTicket: minSyncedTicket,
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

func fromChanges(pbChanges []*api.Change) ([]*change.Change, error) {
	var changes []*change.Change
	for _, pbChange := range pbChanges {
		changeID, err := fromChangeID(pbChange.Id)
		if err != nil {
			return nil, err
		}
		operations, err := FromOperations(pbChange.Operations)
		if err != nil {
			return nil, err
		}
		changes = append(changes, change.New(
			changeID,
			pbChange.Message,
			operations,
		))
	}

	return changes, nil
}

func fromChangeID(id *api.ChangeID) (*change.ID, error) {
	actorID, err := time.ActorIDFromBytes(id.ActorId)
	if err != nil {
		return nil, err
	}
	return change.NewID(
		id.ClientSeq,
		id.Lamport,
		actorID,
	), nil
}

// FromDocumentKeys converts the given Protobuf format to model format.
func FromDocumentKeys(pbKeys []*api.DocumentKey) []*key.Key {
	var keys []*key.Key
	for _, pbKey := range pbKeys {
		keys = append(keys, fromDocumentKey(pbKey))
	}
	return keys
}

// FromEventType converts the given Protobuf format to model format.
func FromEventType(pbDocEventType api.DocEventType) (types.DocEventType, error) {
	switch pbDocEventType {
	case api.DocEventType_DOCUMENTS_CHANGED:
		return types.DocumentsChangedEvent, nil
	case api.DocEventType_DOCUMENTS_WATCHED:
		return types.DocumentsWatchedEvent, nil
	case api.DocEventType_DOCUMENTS_UNWATCHED:
		return types.DocumentsUnwatchedEvent, nil
	}
	return "", errors.Wrapf(ErrUnsupportedEventType, "document event type: %v", pbDocEventType)
}

// FromDocEvent converts the given Protobuf format to model format.
func FromDocEvent(docEvent *api.DocEvent) (*sync.DocEvent, error) {
	client, err := FromClient(docEvent.Publisher)
	if err != nil {
		return nil, err
	}

	eventType, err := FromEventType(docEvent.Type)
	if err != nil {
		return nil, err
	}

	return &sync.DocEvent{
		Type:         eventType,
		Publisher:    *client,
		DocumentKeys: FromDocumentKeys(docEvent.DocumentKeys),
	}, nil
}

// FromOperations converts the given Protobuf format to model format.
func FromOperations(pbOps []*api.Operation) ([]operation.Operation, error) {
	var ops []operation.Operation
	for _, pbOp := range pbOps {
		var op operation.Operation
		var err error
		switch decoded := pbOp.Body.(type) {
		case *api.Operation_Set_:
			op, err = fromSet(decoded.Set)
		case *api.Operation_Add_:
			op, err = fromAdd(decoded.Add)
		case *api.Operation_Move_:
			op, err = fromMove(decoded.Move)
		case *api.Operation_Remove_:
			op, err = fromRemove(decoded.Remove)
		case *api.Operation_Edit_:
			op, err = fromEdit(decoded.Edit)
		case *api.Operation_Select_:
			op, err = fromSelect(decoded.Select)
		case *api.Operation_RichEdit_:
			op, err = fromRichEdit(decoded.RichEdit)
		case *api.Operation_Style_:
			op, err = fromStyle(decoded.Style)
		case *api.Operation_Increase_:
			op, err = fromIncrease(decoded.Increase)
		default:
			return nil, errors.WithStack(ErrUnsupportedOperation)
		}
		if err != nil {
			return nil, err
		}
		ops = append(ops, op)
	}

	return ops, nil
}

func fromSet(pbSet *api.Operation_Set) (*operation.Set, error) {
	parentCreatedAt, err := fromTimeTicket(pbSet.ParentCreatedAt)
	if err != nil {
		return nil, err
	}
	executedAt, err := fromTimeTicket(pbSet.ExecutedAt)
	if err != nil {
		return nil, err
	}
	elem, err := fromElement(pbSet.Value)
	if err != nil {
		return nil, err
	}

	return operation.NewSet(
		parentCreatedAt,
		pbSet.Key,
		elem,
		executedAt,
	), nil
}

func fromAdd(pbAdd *api.Operation_Add) (*operation.Add, error) {
	parentCreatedAt, err := fromTimeTicket(pbAdd.ParentCreatedAt)
	if err != nil {
		return nil, err
	}
	prevCreatedAt, err := fromTimeTicket(pbAdd.PrevCreatedAt)
	if err != nil {
		return nil, err
	}
	elem, err := fromElement(pbAdd.Value)
	if err != nil {
		return nil, err
	}
	executedAt, err := fromTimeTicket(pbAdd.ExecutedAt)
	if err != nil {
		return nil, err
	}
	return operation.NewAdd(
		parentCreatedAt,
		prevCreatedAt,
		elem,
		executedAt,
	), nil
}

func fromMove(pbMove *api.Operation_Move) (*operation.Move, error) {
	parentCreatedAt, err := fromTimeTicket(pbMove.ParentCreatedAt)
	if err != nil {
		return nil, err
	}
	prevCreatedAt, err := fromTimeTicket(pbMove.PrevCreatedAt)
	if err != nil {
		return nil, err
	}
	createdAt, err := fromTimeTicket(pbMove.CreatedAt)
	if err != nil {
		return nil, err
	}
	executedAt, err := fromTimeTicket(pbMove.ExecutedAt)
	if err != nil {
		return nil, err
	}
	return operation.NewMove(
		parentCreatedAt,
		prevCreatedAt,
		createdAt,
		executedAt,
	), nil
}

func fromRemove(pbRemove *api.Operation_Remove) (*operation.Remove, error) {
	parentCreatedAt, err := fromTimeTicket(pbRemove.ParentCreatedAt)
	if err != nil {
		return nil, err
	}
	createdAt, err := fromTimeTicket(pbRemove.CreatedAt)
	if err != nil {
		return nil, err
	}
	executedAt, err := fromTimeTicket(pbRemove.ExecutedAt)
	if err != nil {
		return nil, err
	}
	return operation.NewRemove(
		parentCreatedAt,
		createdAt,
		executedAt,
	), nil
}

func fromEdit(pbEdit *api.Operation_Edit) (*operation.Edit, error) {
	parentCreatedAt, err := fromTimeTicket(pbEdit.ParentCreatedAt)
	if err != nil {
		return nil, err
	}
	from, err := fromTextNodePos(pbEdit.From)
	if err != nil {
		return nil, err
	}
	to, err := fromTextNodePos(pbEdit.To)
	if err != nil {
		return nil, err
	}
	createdAtMapByActor, err := fromCreatedAtMapByActor(
		pbEdit.CreatedAtMapByActor,
	)
	if err != nil {
		return nil, err
	}
	executedAt, err := fromTimeTicket(pbEdit.ExecutedAt)
	if err != nil {
		return nil, err
	}
	return operation.NewEdit(
		parentCreatedAt,
		from,
		to,
		createdAtMapByActor,
		pbEdit.Content,
		executedAt,
	), nil
}

func fromSelect(pbSelect *api.Operation_Select) (*operation.Select, error) {
	parentCreatedAt, err := fromTimeTicket(pbSelect.ParentCreatedAt)
	if err != nil {
		return nil, err
	}
	from, err := fromTextNodePos(pbSelect.From)
	if err != nil {
		return nil, err
	}
	to, err := fromTextNodePos(pbSelect.To)
	if err != nil {
		return nil, err
	}
	executedAt, err := fromTimeTicket(pbSelect.ExecutedAt)
	if err != nil {
		return nil, err
	}
	return operation.NewSelect(
		parentCreatedAt,
		from,
		to,
		executedAt,
	), nil
}

func fromRichEdit(pbEdit *api.Operation_RichEdit) (*operation.RichEdit, error) {
	parentCreatedAt, err := fromTimeTicket(pbEdit.ParentCreatedAt)
	if err != nil {
		return nil, err
	}
	from, err := fromTextNodePos(pbEdit.From)
	if err != nil {
		return nil, err
	}
	to, err := fromTextNodePos(pbEdit.To)
	if err != nil {
		return nil, err
	}
	createdAtMapByActor, err := fromCreatedAtMapByActor(
		pbEdit.CreatedAtMapByActor,
	)
	if err != nil {
		return nil, err
	}
	executedAt, err := fromTimeTicket(pbEdit.ExecutedAt)
	if err != nil {
		return nil, err
	}
	return operation.NewRichEdit(
		parentCreatedAt,
		from,
		to,
		createdAtMapByActor,
		pbEdit.Content,
		pbEdit.Attributes,
		executedAt,
	), nil
}

func fromStyle(pbStyle *api.Operation_Style) (*operation.Style, error) {
	parentCreatedAt, err := fromTimeTicket(pbStyle.ParentCreatedAt)
	if err != nil {
		return nil, err
	}
	from, err := fromTextNodePos(pbStyle.From)
	if err != nil {
		return nil, err
	}
	to, err := fromTextNodePos(pbStyle.To)
	if err != nil {
		return nil, err
	}
	executedAt, err := fromTimeTicket(pbStyle.ExecutedAt)
	if err != nil {
		return nil, err
	}
	return operation.NewStyle(
		parentCreatedAt,
		from,
		to,
		pbStyle.Attributes,
		executedAt,
	), nil
}

func fromIncrease(pbInc *api.Operation_Increase) (*operation.Increase, error) {
	parentCreatedAt, err := fromTimeTicket(pbInc.ParentCreatedAt)
	if err != nil {
		return nil, err
	}
	elem, err := fromElement(pbInc.Value)
	if err != nil {
		return nil, err
	}
	executedAt, err := fromTimeTicket(pbInc.ExecutedAt)
	if err != nil {
		return nil, err
	}
	return operation.NewIncrease(
		parentCreatedAt,
		elem,
		executedAt,
	), nil
}

func fromCreatedAtMapByActor(
	pbCreatedAtMapByActor map[string]*api.TimeTicket,
) (map[string]*time.Ticket, error) {
	createdAtMapByActor := make(map[string]*time.Ticket)
	for actor, pbTicket := range pbCreatedAtMapByActor {
		ticket, err := fromTimeTicket(pbTicket)
		if err != nil {
			return nil, err
		}
		createdAtMapByActor[actor] = ticket
	}
	return createdAtMapByActor, nil
}

func fromTextNodePos(
	pbPos *api.TextNodePos,
) (*json.RGATreeSplitNodePos, error) {
	createdAt, err := fromTimeTicket(pbPos.CreatedAt)
	if err != nil {
		return nil, err
	}
	return json.NewRGATreeSplitNodePos(
		json.NewRGATreeSplitNodeID(createdAt, int(pbPos.Offset)),
		int(pbPos.RelativeOffset),
	), nil
}

func fromTimeTicket(pbTicket *api.TimeTicket) (*time.Ticket, error) {
	if pbTicket == nil {
		return nil, nil
	}

	actorID, err := time.ActorIDFromBytes(pbTicket.ActorId)
	if err != nil {
		return nil, err
	}
	return time.NewTicket(
		pbTicket.Lamport,
		pbTicket.Delimiter,
		actorID,
	), nil
}

func fromElement(pbElement *api.JSONElementSimple) (json.Element, error) {
	switch pbType := pbElement.Type; pbType {
	case api.ValueType_JSON_OBJECT:
		createdAt, err := fromTimeTicket(pbElement.CreatedAt)
		if err != nil {
			return nil, err
		}
		return json.NewObject(
			json.NewRHTPriorityQueueMap(),
			createdAt,
		), nil
	case api.ValueType_JSON_ARRAY:
		createdAt, err := fromTimeTicket(pbElement.CreatedAt)
		if err != nil {
			return nil, err
		}
		return json.NewArray(
			json.NewRGATreeList(),
			createdAt,
		), nil
	case api.ValueType_NULL:
		fallthrough
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
		valueType, err := fromPrimitiveValueType(pbElement.Type)
		if err != nil {
			return nil, err
		}
		createdAt, err := fromTimeTicket(pbElement.CreatedAt)
		if err != nil {
			return nil, err
		}
		return json.NewPrimitive(
			json.ValueFromBytes(valueType, pbElement.Value),
			createdAt,
		), nil
	case api.ValueType_TEXT:
		createdAt, err := fromTimeTicket(pbElement.CreatedAt)
		if err != nil {
			return nil, err
		}
		return json.NewText(
			json.NewRGATreeSplit(json.InitialTextNode()),
			createdAt,
		), nil
	case api.ValueType_RICH_TEXT:
		createdAt, err := fromTimeTicket(pbElement.CreatedAt)
		if err != nil {
			return nil, err
		}
		return json.NewInitialRichText(
			json.NewRGATreeSplit(json.InitialRichTextNode()),
			createdAt,
		), nil
	case api.ValueType_INTEGER_CNT:
		fallthrough
	case api.ValueType_LONG_CNT:
		fallthrough
	case api.ValueType_DOUBLE_CNT:
		counterType, err := fromCounterType(pbType)
		if err != nil {
			return nil, err
		}
		createdAt, err := fromTimeTicket(pbElement.CreatedAt)
		if err != nil {
			return nil, err
		}
		return json.NewCounter(
			json.CounterValueFromBytes(counterType, pbElement.Value),
			createdAt,
		), nil
	}

	return nil, errors.Wrapf(ErrUnsupportedElement, "element type: %d", pbElement.Type)
}

func fromPrimitiveValueType(valueType api.ValueType) (json.ValueType, error) {
	switch valueType {
	case api.ValueType_NULL:
		return json.Null, nil
	case api.ValueType_BOOLEAN:
		return json.Boolean, nil
	case api.ValueType_INTEGER:
		return json.Integer, nil
	case api.ValueType_LONG:
		return json.Long, nil
	case api.ValueType_DOUBLE:
		return json.Double, nil
	case api.ValueType_STRING:
		return json.String, nil
	case api.ValueType_BYTES:
		return json.Bytes, nil
	case api.ValueType_DATE:
		return json.Date, nil
	}

	return 0, errors.Wrapf(ErrUnsupportedValueType, "value type: %d", valueType)
}

func fromCounterType(valueType api.ValueType) (json.CounterType, error) {
	switch valueType {
	case api.ValueType_INTEGER_CNT:
		return json.IntegerCnt, nil
	case api.ValueType_LONG_CNT:
		return json.LongCnt, nil
	case api.ValueType_DOUBLE_CNT:
		return json.DoubleCnt, nil
	}

	return 0, errors.Wrapf(ErrUnsupportedCounterType, "value type: %d", valueType)
}
