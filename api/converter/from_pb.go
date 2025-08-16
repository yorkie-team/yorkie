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
	"strconv"
	"strings"
	gotime "time"

	"github.com/yorkie-team/yorkie/api/types"
	"github.com/yorkie-team/yorkie/api/types/events"
	api "github.com/yorkie-team/yorkie/api/yorkie/v1"
	"github.com/yorkie-team/yorkie/pkg/document/change"
	"github.com/yorkie-team/yorkie/pkg/document/crdt"
	"github.com/yorkie-team/yorkie/pkg/document/innerpresence"
	"github.com/yorkie-team/yorkie/pkg/document/key"
	"github.com/yorkie-team/yorkie/pkg/document/operations"
	"github.com/yorkie-team/yorkie/pkg/document/time"
	"github.com/yorkie-team/yorkie/pkg/resource"
)

var (
	// ErrUnsupportedDateRange is returned when the given date range is unsupported.
	ErrUnsupportedDateRange = fmt.Errorf("unsupported date range")

	// ErrInvalidSchemaKey is returned when the given schema key is invalid.
	ErrInvalidSchemaKey = fmt.Errorf("invalid schema key")
)

// FromUser converts the given Protobuf formats to model format.
func FromUser(pbUser *api.User) *types.User {
	return &types.User{
		ID:           types.ID(pbUser.Id),
		AuthProvider: pbUser.AuthProvider,
		Username:     pbUser.Username,
		CreatedAt:    pbUser.CreatedAt.AsTime(),
	}
}

// FromProjects converts the given Protobuf formats to model format.
func FromProjects(pbProjects []*api.Project) []*types.Project {
	var projects []*types.Project
	for _, pbProject := range pbProjects {
		projects = append(projects, FromProject(pbProject))
	}
	return projects
}

// FromProject converts the given Protobuf formats to model format.
func FromProject(pbProject *api.Project) *types.Project {
	return &types.Project{
		ID:                        types.ID(pbProject.Id),
		Name:                      pbProject.Name,
		AuthWebhookURL:            pbProject.AuthWebhookUrl,
		AuthWebhookMethods:        pbProject.AuthWebhookMethods,
		EventWebhookURL:           pbProject.EventWebhookUrl,
		EventWebhookEvents:        pbProject.EventWebhookEvents,
		ClientDeactivateThreshold: pbProject.ClientDeactivateThreshold,
		MaxSubscribersPerDocument: int(pbProject.MaxSubscribersPerDocument),
		MaxAttachmentsPerDocument: int(pbProject.MaxAttachmentsPerDocument),
		MaxSizePerDocument:        int(pbProject.MaxSizePerDocument),
		AllowedOrigins:            pbProject.AllowedOrigins,
		PublicKey:                 pbProject.PublicKey,
		SecretKey:                 pbProject.SecretKey,
		CreatedAt:                 pbProject.CreatedAt.AsTime(),
		UpdatedAt:                 pbProject.UpdatedAt.AsTime(),
	}
}

// FromDocumentSummaries converts the given Protobuf formats to model format.
func FromDocumentSummaries(pbSummaries []*api.DocumentSummary) []*types.DocumentSummary {
	var summaries []*types.DocumentSummary
	for _, pbSummary := range pbSummaries {
		summaries = append(summaries, FromDocumentSummary(pbSummary))
	}
	return summaries
}

// FromDocumentSummary converts the given Protobuf formats to model format.
func FromDocumentSummary(pbSummary *api.DocumentSummary) *types.DocumentSummary {
	summary := &types.DocumentSummary{
		ID:              types.ID(pbSummary.Id),
		Key:             key.Key(pbSummary.Key),
		AttachedClients: int(pbSummary.AttachedClients),
		CreatedAt:       pbSummary.CreatedAt.AsTime(),
		AccessedAt:      pbSummary.AccessedAt.AsTime(),
		UpdatedAt:       pbSummary.UpdatedAt.AsTime(),
		Root:            pbSummary.Root,
		DocSize:         FromDocSize(pbSummary.DocumentSize),
	}

	if pbSummary.Presences != nil {
		presences := make(map[string]innerpresence.Presence)
		for k, v := range pbSummary.Presences {
			presences[k] = fromPresence(v)
		}
		summary.Presences = presences
	}

	return summary
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

	versionVector, err := FromVersionVector(pbPack.VersionVector)
	if err != nil {
		return nil, err
	}

	pack := &change.Pack{
		DocumentKey:   key.Key(pbPack.DocumentKey),
		Checkpoint:    fromCheckpoint(pbPack.Checkpoint),
		Changes:       changes,
		Snapshot:      pbPack.Snapshot,
		IsRemoved:     pbPack.IsRemoved,
		VersionVector: versionVector,
	}

	return pack, nil
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
			FromPresenceChange(pbChange.PresenceChange),
		))
	}

	return changes, nil
}

func fromChangeID(id *api.ChangeID) (change.ID, error) {
	actorID, err := time.ActorIDFromBytes(id.ActorId)
	if err != nil {
		return change.InitialID(), err
	}

	vector, err := FromVersionVector(id.VersionVector)
	if err != nil {
		return change.InitialID(), err
	}

	return change.NewID(
		id.ClientSeq,
		id.ServerSeq,
		id.Lamport,
		actorID,
		vector,
	), nil
}

// FromVersionVector converts the given Protobuf formats to model format.
func FromVersionVector(pbVersionVector *api.VersionVector) (time.VersionVector, error) {
	versionVector := make(time.VersionVector)
	if pbVersionVector == nil {
		return versionVector, nil
	}
	for id, lamport := range pbVersionVector.Vector {
		actorID, err := time.ActorIDFromBase64(id)
		if err != nil {
			return nil, err
		}
		versionVector.Set(actorID, lamport)
	}

	return versionVector, nil
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
func FromEventType(pbDocEventType api.DocEventType) (events.DocEventType, error) {
	switch pbDocEventType {
	case api.DocEventType_DOC_EVENT_TYPE_DOCUMENT_CHANGED:
		return events.DocChanged, nil
	case api.DocEventType_DOC_EVENT_TYPE_DOCUMENT_WATCHED:
		return events.DocWatched, nil
	case api.DocEventType_DOC_EVENT_TYPE_DOCUMENT_UNWATCHED:
		return events.DocUnwatched, nil
	case api.DocEventType_DOC_EVENT_TYPE_DOCUMENT_BROADCAST:
		return events.DocBroadcast, nil
	}
	return "", fmt.Errorf("%v: %w", pbDocEventType, ErrUnsupportedEventType)
}

// FromDataSize converts the given Protobuf formats to model format.
func FromDataSize(pbDataSize *api.DataSize) resource.DataSize {
	return resource.DataSize{
		Data: int(pbDataSize.Data),
		Meta: int(pbDataSize.Meta),
	}
}

// FromDocSize converts the given Protobuf formats to model format.
func FromDocSize(pbDocSize *api.DocSize) resource.DocSize {
	return resource.DocSize{
		Live: FromDataSize(pbDocSize.Live),
		GC:   FromDataSize(pbDocSize.Gc),
	}
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
		case *api.Operation_Style_:
			op, err = fromStyle(decoded.Style)
		case *api.Operation_Increase_:
			op, err = fromIncrease(decoded.Increase)
		case *api.Operation_TreeEdit_:
			op, err = fromTreeEdit(decoded.TreeEdit)
		case *api.Operation_TreeStyle_:
			op, err = fromTreeStyle(decoded.TreeStyle)
		case *api.Operation_ArraySet_:
			op, err = fromArraySet(decoded.ArraySet)
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

func fromPresences(pbPresences map[string]*api.Presence) *innerpresence.Map {
	presences := innerpresence.NewMap()
	for id, pbPresence := range pbPresences {
		presences.Store(id, fromPresence(pbPresence))
	}
	return presences
}

func fromPresence(pbPresence *api.Presence) innerpresence.Presence {
	if pbPresence == nil {
		return nil
	}

	data := pbPresence.GetData()
	if data == nil {
		data = innerpresence.New()
	}

	return data
}

// FromPresenceChange converts the given Protobuf formats to model format.
func FromPresenceChange(pbPresenceChange *api.PresenceChange) *innerpresence.Change {
	if pbPresenceChange == nil {
		return nil
	}

	var p innerpresence.Change
	switch pbPresenceChange.Type {
	case api.PresenceChange_CHANGE_TYPE_PUT:
		p = innerpresence.Change{
			ChangeType: innerpresence.Put,
			Presence:   pbPresenceChange.Presence.Data,
		}
		if p.Presence == nil {
			p.Presence = innerpresence.New()
		}
	case api.PresenceChange_CHANGE_TYPE_CLEAR:
		p = innerpresence.Change{
			ChangeType: innerpresence.Clear,
			Presence:   nil,
		}
	}

	return &p
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
	executedAt, err := fromTimeTicket(pbEdit.ExecutedAt)
	if err != nil {
		return nil, err
	}
	return operations.NewEdit(
		parentCreatedAt,
		from,
		to,
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

	nodes, err := FromTreeNodesWhenEdit(pbTreeEdit.Contents)
	if err != nil {
		return nil, err
	}

	return operations.NewTreeEdit(
		parentCreatedAt,
		from,
		to,
		nodes,
		int(pbTreeEdit.SplitLevel),
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

	if len(pbTreeStyle.AttributesToRemove) > 0 {
		return operations.NewTreeStyleRemove(
			parentCreatedAt,
			from,
			to,
			pbTreeStyle.AttributesToRemove,
			executedAt,
		), nil
	}

	return operations.NewTreeStyle(
		parentCreatedAt,
		from,
		to,
		pbTreeStyle.Attributes,
		executedAt,
	), nil
}

func fromArraySet(pbSetByIndex *api.Operation_ArraySet) (*operations.ArraySet, error) {
	parentCreatedAt, err := fromTimeTicket(pbSetByIndex.ParentCreatedAt)
	if err != nil {
		return nil, err
	}
	createdAt, err := fromTimeTicket(pbSetByIndex.CreatedAt)
	if err != nil {
		return nil, err
	}
	elem, err := fromElement(pbSetByIndex.Value)
	if err != nil {
		return nil, err
	}
	executedAt, err := fromTimeTicket(pbSetByIndex.ExecutedAt)
	if err != nil {
		return nil, err
	}
	return operations.NewArraySet(
		parentCreatedAt,
		createdAt,
		elem,
		executedAt,
	), nil
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
	depthTable := make(map[int32]*crdt.TreeNode)
	depthTable[pbNodes[len(nodes)-1].Depth] = nodes[len(nodes)-1]
	for i := len(nodes) - 2; i >= 0; i-- {
		var parent *crdt.TreeNode = depthTable[pbNodes[i].Depth-1]

		if err := parent.Prepend(nodes[i]); err != nil {
			return nil, err
		}
		depthTable[pbNodes[i].Depth] = nodes[i]
	}

	root.Index.UpdateDescendantsSize()

	// build crdt.Tree from root to construct the links between nodes.
	return crdt.NewTree(root, nil).Root(), nil
}

// FromTreeNodesWhenEdit converts protobuf tree nodes to array of crdt.TreeNode.
// in each element in array, the last node in slice is the root node, because the slice is in post-order.
func FromTreeNodesWhenEdit(pbNodes []*api.TreeNodes) ([]*crdt.TreeNode, error) {
	if len(pbNodes) == 0 {
		return nil, nil
	}

	var treeNodes []*crdt.TreeNode

	for _, pbNode := range pbNodes {
		treeNode, err := FromTreeNodes(pbNode.Content)

		if err != nil {
			return nil, err
		}

		treeNodes = append(treeNodes, treeNode)
	}

	return treeNodes, nil
}

func fromRHT(pbRHT map[string]*api.NodeAttr) (*crdt.RHT, error) {
	rht := crdt.NewRHT()
	for k, pbAttr := range pbRHT {
		updatedAt, err := fromTimeTicket(pbAttr.UpdatedAt)
		if err != nil {
			return nil, err
		}
		rht.SetInternal(k, pbAttr.Value, updatedAt, pbAttr.IsRemoved)
	}
	return rht, nil
}

func fromTreeNode(pbNode *api.TreeNode) (*crdt.TreeNode, error) {
	id, err := fromTreeNodeID(pbNode.Id)
	if err != nil {
		return nil, err
	}

	attrs, err := fromRHT(pbNode.Attributes)
	if err != nil {
		return nil, err
	}

	node := crdt.NewTreeNode(
		id,
		pbNode.Type,
		attrs,
		pbNode.Value,
	)

	if pbNode.GetInsPrevId() != nil {
		node.InsPrevID, err = fromTreeNodeID(pbNode.GetInsPrevId())
		if err != nil {
			return nil, err
		}
	}

	if pbNode.GetInsNextId() != nil {
		node.InsNextID, err = fromTreeNodeID(pbNode.GetInsNextId())
		if err != nil {
			return nil, err
		}
	}

	removedAt, err := fromTimeTicket(pbNode.RemovedAt)
	if err != nil {
		return nil, err
	}
	node.SetRemovedAt(removedAt)

	return node, nil
}

func fromTreePos(pbPos *api.TreePos) (*crdt.TreePos, error) {
	parentID, err := fromTreeNodeID(pbPos.ParentId)
	if err != nil {
		return nil, err
	}

	leftSiblingID, err := fromTreeNodeID(pbPos.LeftSiblingId)
	if err != nil {
		return nil, err
	}

	return crdt.NewTreePos(parentID, leftSiblingID), nil
}

func fromTreeNodeID(pbPos *api.TreeNodeID) (*crdt.TreeNodeID, error) {
	createdAt, err := fromTimeTicket(pbPos.CreatedAt)
	if err != nil {
		return nil, err
	}

	return crdt.NewTreeNodeID(
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
		if pbElement.Value == nil {
			createdAt, err := fromTimeTicket(pbElement.CreatedAt)
			if err != nil {
				return nil, err
			}
			return crdt.NewObject(
				crdt.NewElementRHT(),
				createdAt,
			), nil
		}
		return BytesToObject(pbElement.Value)
	case api.ValueType_VALUE_TYPE_JSON_ARRAY:
		if pbElement.Value == nil {
			createdAt, err := fromTimeTicket(pbElement.CreatedAt)
			if err != nil {
				return nil, err
			}
			elements := crdt.NewRGATreeList()
			return crdt.NewArray(elements, createdAt), nil
		}
		return BytesToArray(pbElement.Value)
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
		value, err := crdt.ValueFromBytes(valueType, pbElement.Value)
		if err != nil {
			return nil, err
		}
		primitive, err := crdt.NewPrimitive(value, createdAt)
		if err != nil {
			return nil, err
		}
		return primitive, nil
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
		counterValue, err := crdt.CounterValueFromBytes(counterType, pbElement.Value)
		if err != nil {
			return nil, err
		}

		counter, err := crdt.NewCounter(
			counterType,
			counterValue,
			createdAt,
		)
		if err != nil {
			return nil, err
		}
		return counter, nil
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
	if pbProjectFields.EventWebhookUrl != nil {
		updatableProjectFields.EventWebhookURL = &pbProjectFields.EventWebhookUrl.Value
	}
	if pbProjectFields.EventWebhookEvents != nil {
		updatableProjectFields.EventWebhookEvents = &pbProjectFields.EventWebhookEvents.Events
	}
	if pbProjectFields.ClientDeactivateThreshold != nil {
		updatableProjectFields.ClientDeactivateThreshold = &pbProjectFields.ClientDeactivateThreshold.Value
	}
	if pbProjectFields.MaxSubscribersPerDocument != nil {
		value := int(pbProjectFields.MaxSubscribersPerDocument.Value)
		updatableProjectFields.MaxSubscribersPerDocument = &value
	}
	if pbProjectFields.MaxAttachmentsPerDocument != nil {
		value := int(pbProjectFields.MaxAttachmentsPerDocument.Value)
		updatableProjectFields.MaxAttachmentsPerDocument = &value
	}
	if pbProjectFields.MaxSizePerDocument != nil {
		value := int(pbProjectFields.MaxSizePerDocument.Value)
		updatableProjectFields.MaxSizePerDocument = &value
	}
	if pbProjectFields.AllowedOrigins != nil {
		updatableProjectFields.AllowedOrigins = &pbProjectFields.AllowedOrigins.Origins
	}

	return updatableProjectFields, nil
}

// FromDateRange converts the given Protobuf formats to model format.
func FromDateRange(
	pbRange api.GetProjectStatsRequest_DateRange,
) (gotime.Time, gotime.Time, error) {
	// NOTE(hackerwins): The end time is exclusive in the query. So, we need to
	// add 1 day to the end time.
	var from, to gotime.Time
	now := gotime.Now()

	switch pbRange {
	case api.GetProjectStatsRequest_DATE_RANGE_LAST_1W:
		from = now.AddDate(0, 0, -7)
		to = now.AddDate(0, 0, 1)
	case api.GetProjectStatsRequest_DATE_RANGE_LAST_4W:
		from = now.AddDate(0, 0, -28)
		to = now.AddDate(0, 0, 1)
	case api.GetProjectStatsRequest_DATE_RANGE_LAST_3M:
		from = now.AddDate(0, -3, 0)
		to = now.AddDate(0, 0, 1)
	case api.GetProjectStatsRequest_DATE_RANGE_LAST_12M:
		from = now.AddDate(0, -12, 0)
		to = now.AddDate(0, 0, 1)
	default:
		return from, to, fmt.Errorf("%v, %w", pbRange, ErrUnsupportedDateRange)
	}

	return from, to, nil
}

// FromSchemaKey converts the given Protobuf formats to model format.
func FromSchemaKey(schemaKey string) (string, int, error) {
	if schemaKey == "" {
		return "", 0, nil
	}

	split := strings.Split(schemaKey, "@")
	if len(split) != 2 {
		return "", 0, fmt.Errorf("parse schema %s: %w", schemaKey, ErrInvalidSchemaKey)
	}

	name := split[0]
	version, err := strconv.Atoi(split[1])
	if err != nil {
		return "", 0, fmt.Errorf("parse version %s: %w", split[1], ErrInvalidSchemaKey)
	}

	return name, version, nil
}

func FromRules(pbRules []*api.Rule) []types.Rule {
	var rules []types.Rule
	for _, pbRule := range pbRules {
		rules = append(rules, FromRule(pbRule))
	}
	return rules
}

// FromRule converts the given Protobuf formats to model format.
func FromRule(pbRule *api.Rule) types.Rule {
	return types.Rule{
		Path: pbRule.Path,
		Type: pbRule.Type,
	}
}

// FromSchemas converts the given Protobuf formats to model format.
func FromSchemas(pbSchemas []*api.Schema) []*types.Schema {
	var schemas []*types.Schema
	for _, pbSchema := range pbSchemas {
		schemas = append(schemas, FromSchema(pbSchema))
	}
	return schemas
}

// FromSchema converts the given Protobuf formats to model format.
func FromSchema(pbSchema *api.Schema) *types.Schema {
	return &types.Schema{
		ID:        types.ID(pbSchema.Id),
		Name:      pbSchema.Name,
		Version:   int(pbSchema.Version),
		Body:      pbSchema.Body,
		Rules:     FromRules(pbSchema.Rules),
		CreatedAt: pbSchema.CreatedAt.AsTime(),
	}
}
