package converter

import (
	"errors"

	"github.com/hackerwins/yorkie/api"
	"github.com/hackerwins/yorkie/pkg/document/change"
	"github.com/hackerwins/yorkie/pkg/document/checkpoint"
	"github.com/hackerwins/yorkie/pkg/document/json"
	"github.com/hackerwins/yorkie/pkg/document/key"
	"github.com/hackerwins/yorkie/pkg/document/operation"
	"github.com/hackerwins/yorkie/pkg/document/time"
	"github.com/hackerwins/yorkie/pkg/log"
)

var (
	errPackRequired       = errors.New("pack required")
	errCheckpointRequired = errors.New("checkpoint required")
)

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
		DocumentKey: fromDocumentKey(pbPack.DocumentKey),
		Checkpoint:  fromCheckpoint(pbPack.Checkpoint),
		Changes:     fromChanges(pbPack.Changes),
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

func FromDocumentKeys(pbKeys []*api.DocumentKey) []*key.Key {
	var keys []*key.Key
	for _, pbKey := range pbKeys {
		keys = append(keys, fromDocumentKey(pbKey))
	}
	return keys
}

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

func fromTextNodePos(pbPos *api.TextNodePos) *json.TextNodePos {
	return json.NewTextNodePos(
		json.NewTextNodeID(fromTimeTicket(pbPos.CreatedAt), int(pbPos.Offset)),
		int(pbPos.RelativeOffset),
	)
}

func fromTimeTicket(pbTicket *api.TimeTicket) *time.Ticket {
	return time.NewTicket(
		pbTicket.Lamport,
		pbTicket.Delimiter,
		time.ActorIDFromHex(pbTicket.ActorId),
	)
}

func fromElement(pbElement *api.JSONElement) json.Element {
	switch pbElement.Type {
	case api.ValueType_JSON_OBJECT:
		return json.NewObject(
			json.NewRHT(),
			fromTimeTicket(pbElement.CreatedAt),
		)
	case api.ValueType_JSON_ARRAY:
		return json.NewArray(
			json.NewRGA(),
			fromTimeTicket(pbElement.CreatedAt),
		)
	case api.ValueType_BOOLEAN:
		return json.NewPrimitive(
			json.ValueFromBytes(json.Boolean, pbElement.Value),
			fromTimeTicket(pbElement.CreatedAt),
		)
	case api.ValueType_INTEGER:
		return json.NewPrimitive(
			json.ValueFromBytes(json.Integer, pbElement.Value),
			fromTimeTicket(pbElement.CreatedAt),
		)
	case api.ValueType_LONG:
		return json.NewPrimitive(
			json.ValueFromBytes(json.Long, pbElement.Value),
			fromTimeTicket(pbElement.CreatedAt),
		)
	case api.ValueType_DOUBLE:
		return json.NewPrimitive(
			json.ValueFromBytes(json.Double, pbElement.Value),
			fromTimeTicket(pbElement.CreatedAt),
		)
	case api.ValueType_STRING:
		return json.NewPrimitive(
			json.ValueFromBytes(json.String, pbElement.Value),
			fromTimeTicket(pbElement.CreatedAt),
		)
	case api.ValueType_BYTES:
		return json.NewPrimitive(
			json.ValueFromBytes(json.Bytes, pbElement.Value),
			fromTimeTicket(pbElement.CreatedAt),
		)
	case api.ValueType_DATE:
		return json.NewPrimitive(
			json.ValueFromBytes(json.Date, pbElement.Value),
			fromTimeTicket(pbElement.CreatedAt),
		)
	case api.ValueType_TEXT:
		return json.NewText(
			json.NewRGATreeSplit(),
			fromTimeTicket(pbElement.CreatedAt),
		)
	}

	panic("fail to decode element")
}
