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

	protoTypes "github.com/gogo/protobuf/types"

	"github.com/yorkie-team/yorkie/api/types"
	api "github.com/yorkie-team/yorkie/api/yorkie/v1"
	"github.com/yorkie-team/yorkie/pkg/document/change"
	"github.com/yorkie-team/yorkie/pkg/document/crdt"
	"github.com/yorkie-team/yorkie/pkg/document/key"
	"github.com/yorkie-team/yorkie/pkg/document/operations"
	"github.com/yorkie-team/yorkie/pkg/document/time"
	"github.com/yorkie-team/yorkie/server/backend/sync"
)

// FromUser converts the given Protobuf formats to model format.
func FromUser(pbUser *api.User) (*types.User, error) {
	createdAt, err := protoTypes.TimestampFromProto(pbUser.CreatedAt)
	if err != nil {
		return nil, fmt.Errorf("convert createdAt to timestamp: %w", err)
	}

	return &types.User{
		ID:        types.ID(pbUser.Id),
		Username:  pbUser.Username,
		CreatedAt: createdAt,
	}, nil
}

// FromProjects converts the given Protobuf formats to model format.
func FromProjects(pbProjects []*api.Project) ([]*types.Project, error) {
	var projects []*types.Project
	for _, pbProject := range pbProjects {
		project, err := FromProject(pbProject)
		if err != nil {
			return nil, err
		}
		projects = append(projects, project)
	}
	return projects, nil
}

// FromProject converts the given Protobuf formats to model format.
func FromProject(pbProject *api.Project) (*types.Project, error) {
	createdAt, err := protoTypes.TimestampFromProto(pbProject.CreatedAt)
	if err != nil {
		return nil, fmt.Errorf("convert createdAt to timestamp: %w", err)
	}
	updatedAt, err := protoTypes.TimestampFromProto(pbProject.UpdatedAt)
	if err != nil {
		return nil, fmt.Errorf("convert updatedAt to timestamp: %w", err)
	}

	return &types.Project{
		ID:                        types.ID(pbProject.Id),
		Name:                      pbProject.Name,
		AuthWebhookURL:            pbProject.AuthWebhookUrl,
		AuthWebhookMethods:        pbProject.AuthWebhookMethods,
		ClientDeactivateThreshold: pbProject.ClientDeactivateThreshold,
		PublicKey:                 pbProject.PublicKey,
		SecretKey:                 pbProject.SecretKey,
		CreatedAt:                 createdAt,
		UpdatedAt:                 updatedAt,
	}, nil
}

// FromDocumentSummaries converts the given Protobuf formats to model format.
func FromDocumentSummaries(pbSummaries []*api.DocumentSummary) ([]*types.DocumentSummary, error) {
	var summaries []*types.DocumentSummary
	for _, pbSummary := range pbSummaries {
		summary, err := FromDocumentSummary(pbSummary)
		if err != nil {
			return nil, err
		}
		summaries = append(summaries, summary)
	}
	return summaries, nil
}

// FromDocumentSummary converts the given Protobuf formats to model format.
func FromDocumentSummary(pbSummary *api.DocumentSummary) (*types.DocumentSummary, error) {
	createdAt, err := protoTypes.TimestampFromProto(pbSummary.CreatedAt)
	if err != nil {
		return nil, fmt.Errorf("convert createdAt to timestamp: %w", err)
	}
	accessedAt, err := protoTypes.TimestampFromProto(pbSummary.AccessedAt)
	if err != nil {
		return nil, fmt.Errorf("convert accessedAt to timestamp: %w", err)
	}
	updatedAt, err := protoTypes.TimestampFromProto(pbSummary.UpdatedAt)
	if err != nil {
		return nil, fmt.Errorf("convert updatedAt to timestamp: %w", err)
	}
	return &types.DocumentSummary{
		ID:         types.ID(pbSummary.Id),
		Key:        key.Key(pbSummary.Key),
		CreatedAt:  createdAt,
		AccessedAt: accessedAt,
		UpdatedAt:  updatedAt,
		Snapshot:   pbSummary.Snapshot,
	}, nil
}

// FromClient converts the given Protobuf formats to model format.
func FromClient(pbClient *api.Client) (*types.Client, error) {
	id, err := time.ActorIDFromBytes(pbClient.Id)
	if err != nil {
		return nil, err
	}

	return &types.Client{
		ID:           id,
		PresenceInfo: FromPresenceInfo(pbClient.Presence),
	}, nil
}

// FromPresenceInfo converts the given Protobuf formats to model format.
func FromPresenceInfo(pbPresence *api.Presence) types.PresenceInfo {
	return types.PresenceInfo{
		Clock:    pbPresence.Clock,
		Presence: pbPresence.Data,
	}
}

// FromChangePack converts the given Protobuf formats to model format.
func FromChangePack(pbPack *api.ChangePack) (*change.Pack, error) {
	if pbPack == nil {
		return nil, ErrPackRequired
	}
	if pbPack.Checkpoint == nil {
		return nil, ErrCheckpointRequired
	}

	changes, err := FromChanges(pbPack.Changes)
	if err != nil {
		return nil, err
	}

	minSyncedTicket, err := fromTimeTicket(pbPack.MinSyncedTicket)
	if err != nil {
		return nil, err
	}

	return &change.Pack{
		DocumentKey:     key.Key(pbPack.DocumentKey),
		Checkpoint:      fromCheckpoint(pbPack.Checkpoint),
		Changes:         changes,
		Snapshot:        pbPack.Snapshot,
		MinSyncedTicket: minSyncedTicket,
		IsRemoved:       pbPack.IsRemoved,
	}, nil
}

func fromCheckpoint(pbCheckpoint *api.Checkpoint) change.Checkpoint {
	return change.NewCheckpoint(
		pbCheckpoint.ServerSeq,
		pbCheckpoint.ClientSeq,
	)
}

// FromChanges converts the given Protobuf formats to model format.
func FromChanges(pbChanges []*api.Change) ([]*change.Change, error) {
	var changes []*change.Change
	for _, pbChange := range pbChanges {
		changeID, err := fromChangeID(pbChange.Id)
		if err != nil {
			return nil, err
		}
		ops, err := FromOperations(pbChange.Operations)
		if err != nil {
			return nil, err
		}
		changes = append(changes, change.New(
			changeID,
			pbChange.Message,
			ops,
		))
	}

	return changes, nil
}

func fromChangeID(id *api.ChangeID) (change.ID, error) {
	actorID, err := time.ActorIDFromBytes(id.ActorId)
	if err != nil {
		return change.InitialID, err
	}
	return change.NewID(
		id.ClientSeq,
		id.ServerSeq,
		id.Lamport,
		actorID,
	), nil
}

// FromDocumentKey converts the given Protobuf formats to model format.
func FromDocumentKey(pbKey string) (key.Key, error) {
	k := key.Key(pbKey)
	if err := k.Validate(); err != nil {
		return "", err
	}

	return k, nil
}

// FromDocumentID converts the given Protobuf formats to model format.
func FromDocumentID(pbID string) (types.ID, error) {
	id := types.ID(pbID)
	if err := id.Validate(); err != nil {
		return "", err
	}

	return id, nil
}

// FromEventType converts the given Protobuf formats to model format.
func FromEventType(pbDocEventType api.DocEventType) (types.DocEventType, error) {
	switch pbDocEventType {
	case api.DocEventType_DOC_EVENT_TYPE_DOCUMENTS_CHANGED:
		return types.DocumentsChangedEvent, nil
	case api.DocEventType_DOC_EVENT_TYPE_DOCUMENTS_WATCHED:
		return types.DocumentsWatchedEvent, nil
	case api.DocEventType_DOC_EVENT_TYPE_DOCUMENTS_UNWATCHED:
		return types.DocumentsUnwatchedEvent, nil
	case api.DocEventType_DOC_EVENT_TYPE_PRESENCE_CHANGED:
		return types.PresenceChangedEvent, nil
	}
	return "", fmt.Errorf("%v: %w", pbDocEventType, ErrUnsupportedEventType)
}

// FromDocEvent converts the given Protobuf formats to model format.
func FromDocEvent(docEvent *api.DocEvent) (*sync.DocEvent, error) {
	client, err := FromClient(docEvent.Publisher)
	if err != nil {
		return nil, err
	}

	eventType, err := FromEventType(docEvent.Type)
	if err != nil {
		return nil, err
	}

	documentID, err := FromDocumentID(docEvent.DocumentId)
	if err != nil {
		return nil, err
	}

	return &sync.DocEvent{
		Type:       eventType,
		Publisher:  *client,
		DocumentID: documentID,
	}, nil
}

// FromClients converts the given Protobuf formats to model format.
func FromClients(pbClients []*api.Client) ([]*types.Client, error) {
	var clients []*types.Client
	for _, pbClient := range pbClients {
		client, err := FromClient(pbClient)
		if err != nil {
			return nil, err
		}
		clients = append(clients, client)
	}

	return clients, nil
}

// FromOperations converts the given Protobuf formats to model format.
func FromOperations(pbOps []*api.Operation) ([]operations.Operation, error) {
	var ops []operations.Operation
	for _, pbOp := range pbOps {
		var op operations.Operation
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
		case *api.Operation_Style_:
			op, err = fromStyle(decoded.Style)
		case *api.Operation_Increase_:
			op, err = fromIncrease(decoded.Increase)
		case *api.Operation_TreeEdit_:
			op, err = fromTreeEdit(decoded.TreeEdit)
		case *api.Operation_TreeStyle_:
			op, err = fromTreeStyle(decoded.TreeStyle)
		default:
			return nil, ErrUnsupportedOperation
		}
		if err != nil {
			return nil, err
		}
		ops = append(ops, op)
	}

	return ops, nil
}

func fromSet(pbSet *api.Operation_Set) (*operations.Set, error) {
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

	return operations.NewSet(
		parentCreatedAt,
		pbSet.Key,
		elem,
		executedAt,
	), nil
}

func fromAdd(pbAdd *api.Operation_Add) (*operations.Add, error) {
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
	return operations.NewAdd(
		parentCreatedAt,
		prevCreatedAt,
		elem,
		executedAt,
	), nil
}

func fromMove(pbMove *api.Operation_Move) (*operations.Move, error) {
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
	return operations.NewMove(
		parentCreatedAt,
		prevCreatedAt,
		createdAt,
		executedAt,
	), nil
}

func fromRemove(pbRemove *api.Operation_Remove) (*operations.Remove, error) {
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
	return operations.NewRemove(
		parentCreatedAt,
		createdAt,
		executedAt,
	), nil
}

func fromSelect(pbSelect *api.Operation_Select) (*operations.Select, error) {
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
	return operations.NewSelect(
		parentCreatedAt,
		from,
		to,
		executedAt,
	), nil
}

func fromEdit(pbEdit *api.Operation_Edit) (*operations.Edit, error) {
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
	return operations.NewEdit(
		parentCreatedAt,
		from,
		to,
		createdAtMapByActor,
		pbEdit.Content,
		pbEdit.Attributes,
		executedAt,
	), nil
}

func fromStyle(pbStyle *api.Operation_Style) (*operations.Style, error) {
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
	return operations.NewStyle(
		parentCreatedAt,
		from,
		to,
		pbStyle.Attributes,
		executedAt,
	), nil
}

func fromIncrease(pbInc *api.Operation_Increase) (*operations.Increase, error) {
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
	return operations.NewIncrease(
		parentCreatedAt,
		elem,
		executedAt,
	), nil
}

func fromTreeEdit(pbTreeEdit *api.Operation_TreeEdit) (*operations.TreeEdit, error) {
	parentCreatedAt, err := fromTimeTicket(pbTreeEdit.ParentCreatedAt)
	if err != nil {
		return nil, err
	}

	executedAt, err := fromTimeTicket(pbTreeEdit.ExecutedAt)
	if err != nil {
		return nil, err
	}

	from, err := fromTreePos(pbTreeEdit.From)
	if err != nil {
		return nil, err
	}

	to, err := fromTreePos(pbTreeEdit.To)
	if err != nil {
		return nil, err
	}

	node, err := FromTreeNodes(pbTreeEdit.Content)
	if err != nil {
		return nil, err
	}

	return operations.NewTreeEdit(
		parentCreatedAt,
		from,
		to,
		node,
		executedAt,
	), nil
}

func fromTreeStyle(pbTreeStyle *api.Operation_TreeStyle) (*operations.TreeStyle, error) {
	parentCreatedAt, err := fromTimeTicket(pbTreeStyle.ParentCreatedAt)
	if err != nil {
		return nil, err
	}

	executedAt, err := fromTimeTicket(pbTreeStyle.ExecutedAt)
	if err != nil {
		return nil, err
	}

	from, err := fromTreePos(pbTreeStyle.From)
	if err != nil {
		return nil, err
	}

	to, err := fromTreePos(pbTreeStyle.To)
	if err != nil {
		return nil, err
	}

	return operations.NewTreeStyle(
		parentCreatedAt,
		from,
		to,
		pbTreeStyle.Attributes,
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
) (*crdt.RGATreeSplitNodePos, error) {
	createdAt, err := fromTimeTicket(pbPos.CreatedAt)
	if err != nil {
		return nil, err
	}
	return crdt.NewRGATreeSplitNodePos(
		crdt.NewRGATreeSplitNodeID(createdAt, int(pbPos.Offset)),
		int(pbPos.RelativeOffset),
	), nil
}

// FromTreeNodes converts protobuf tree nodes to crdt.TreeNode. The last node
// in the slice is the root node, because the slice is in post-order.
func FromTreeNodes(pbNodes []*api.TreeNode) (*crdt.TreeNode, error) {
	if len(pbNodes) == 0 {
		return nil, nil
	}

	nodes := make([]*crdt.TreeNode, len(pbNodes))
	for i, pbNode := range pbNodes {
		node, err := fromTreeNode(pbNode)
		if err != nil {
			return nil, err
		}
		nodes[i] = node
	}

	root := nodes[len(nodes)-1]
	for i := len(nodes) - 2; i >= 0; i-- {
		var parent *crdt.TreeNode
		for j := i + 1; j < len(nodes); j++ {
			if pbNodes[i].Depth-1 == pbNodes[j].Depth {
				parent = nodes[j]
				break
			}
		}

		if err := parent.Prepend(nodes[i]); err != nil {
			return nil, err
		}
	}

	// build crdt.Tree from root to construct the links between nodes.
	return crdt.NewTree(root, nil).Root(), nil
}

func fromTreeNode(pbNode *api.TreeNode) (*crdt.TreeNode, error) {
	pos, err := fromTreePos(pbNode.Pos)
	if err != nil {
		return nil, err
	}

	attrs := crdt.NewRHT()
	for k, pbAttr := range pbNode.Attributes {
		updatedAt, err := fromTimeTicket(pbAttr.UpdatedAt)
		if err != nil {
			return nil, err
		}
		attrs.Set(k, pbAttr.Value, updatedAt)
	}

	return crdt.NewTreeNode(
		pos,
		pbNode.Type,
		attrs,
		pbNode.Value,
	), nil
}

func fromTreePos(pbPos *api.TreePos) (*crdt.TreePos, error) {
	createdAt, err := fromTimeTicket(pbPos.CreatedAt)
	if err != nil {
		return nil, err
	}

	return crdt.NewTreePos(
		createdAt,
		int(pbPos.Offset),
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

func fromElement(pbElement *api.JSONElementSimple) (crdt.Element, error) {
	switch pbType := pbElement.Type; pbType {
	case api.ValueType_VALUE_TYPE_JSON_OBJECT:
		createdAt, err := fromTimeTicket(pbElement.CreatedAt)
		if err != nil {
			return nil, err
		}
		return crdt.NewObject(
			crdt.NewElementRHT(),
			createdAt,
		), nil
	case api.ValueType_VALUE_TYPE_JSON_ARRAY:
		createdAt, err := fromTimeTicket(pbElement.CreatedAt)
		if err != nil {
			return nil, err
		}
		return crdt.NewArray(
			crdt.NewRGATreeList(),
			createdAt,
		), nil
	case api.ValueType_VALUE_TYPE_NULL:
		fallthrough
	case api.ValueType_VALUE_TYPE_BOOLEAN:
		fallthrough
	case api.ValueType_VALUE_TYPE_INTEGER:
		fallthrough
	case api.ValueType_VALUE_TYPE_LONG:
		fallthrough
	case api.ValueType_VALUE_TYPE_DOUBLE:
		fallthrough
	case api.ValueType_VALUE_TYPE_STRING:
		fallthrough
	case api.ValueType_VALUE_TYPE_BYTES:
		fallthrough
	case api.ValueType_VALUE_TYPE_DATE:
		valueType, err := fromPrimitiveValueType(pbElement.Type)
		if err != nil {
			return nil, err
		}
		createdAt, err := fromTimeTicket(pbElement.CreatedAt)
		if err != nil {
			return nil, err
		}
		return crdt.NewPrimitive(
			crdt.ValueFromBytes(valueType, pbElement.Value),
			createdAt,
		), nil
	case api.ValueType_VALUE_TYPE_TEXT:
		createdAt, err := fromTimeTicket(pbElement.CreatedAt)
		if err != nil {
			return nil, err
		}
		return crdt.NewText(
			crdt.NewRGATreeSplit(crdt.InitialTextNode()),
			createdAt,
		), nil
	case api.ValueType_VALUE_TYPE_INTEGER_CNT:
		fallthrough
	case api.ValueType_VALUE_TYPE_LONG_CNT:
		counterType, err := fromCounterType(pbType)
		if err != nil {
			return nil, err
		}
		createdAt, err := fromTimeTicket(pbElement.CreatedAt)
		if err != nil {
			return nil, err
		}
		return crdt.NewCounter(
			counterType,
			crdt.CounterValueFromBytes(counterType, pbElement.Value),
			createdAt,
		), nil
	case api.ValueType_VALUE_TYPE_TREE:
		return BytesToTree(pbElement.Value)
	}

	return nil, fmt.Errorf("%d, %w", pbElement.Type, ErrUnsupportedElement)
}

func fromPrimitiveValueType(valueType api.ValueType) (crdt.ValueType, error) {
	switch valueType {
	case api.ValueType_VALUE_TYPE_NULL:
		return crdt.Null, nil
	case api.ValueType_VALUE_TYPE_BOOLEAN:
		return crdt.Boolean, nil
	case api.ValueType_VALUE_TYPE_INTEGER:
		return crdt.Integer, nil
	case api.ValueType_VALUE_TYPE_LONG:
		return crdt.Long, nil
	case api.ValueType_VALUE_TYPE_DOUBLE:
		return crdt.Double, nil
	case api.ValueType_VALUE_TYPE_STRING:
		return crdt.String, nil
	case api.ValueType_VALUE_TYPE_BYTES:
		return crdt.Bytes, nil
	case api.ValueType_VALUE_TYPE_DATE:
		return crdt.Date, nil
	}

	return 0, fmt.Errorf("%d, %w", valueType, ErrUnsupportedValueType)
}

func fromCounterType(valueType api.ValueType) (crdt.CounterType, error) {
	switch valueType {
	case api.ValueType_VALUE_TYPE_INTEGER_CNT:
		return crdt.IntegerCnt, nil
	case api.ValueType_VALUE_TYPE_LONG_CNT:
		return crdt.LongCnt, nil
	}

	return 0, fmt.Errorf("%d, %w", valueType, ErrUnsupportedCounterType)
}

// FromUpdatableProjectFields converts the given Protobuf formats to model format.
func FromUpdatableProjectFields(pbProjectFields *api.UpdatableProjectFields) (*types.UpdatableProjectFields, error) {
	updatableProjectFields := &types.UpdatableProjectFields{}
	if pbProjectFields.Name != nil {
		updatableProjectFields.Name = &pbProjectFields.Name.Value
	}
	if pbProjectFields.AuthWebhookUrl != nil {
		updatableProjectFields.AuthWebhookURL = &pbProjectFields.AuthWebhookUrl.Value
	}
	if pbProjectFields.AuthWebhookMethods != nil {
		updatableProjectFields.AuthWebhookMethods = &pbProjectFields.AuthWebhookMethods.Methods
	}
	if pbProjectFields.ClientDeactivateThreshold != nil {
		updatableProjectFields.ClientDeactivateThreshold = &pbProjectFields.ClientDeactivateThreshold.Value
	}

	return updatableProjectFields, nil
}
