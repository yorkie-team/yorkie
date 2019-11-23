package converter

import (
	"github.com/hackerwins/yorkie/api"
	"github.com/hackerwins/yorkie/pkg/document/change"
	"github.com/hackerwins/yorkie/pkg/document/checkpoint"
	"github.com/hackerwins/yorkie/pkg/document/json"
	"github.com/hackerwins/yorkie/pkg/document/json/datatype"
	"github.com/hackerwins/yorkie/pkg/document/key"
	"github.com/hackerwins/yorkie/pkg/document/operation"
	"github.com/hackerwins/yorkie/pkg/document/time"
)

func FromChangePack(pbPack *api.ChangePack) *change.Pack {
	return &change.Pack{
		DocumentKey: fromDocumentKey(pbPack.DocumentKey),
		Checkpoint:  fromCheckpoint(pbPack.Checkpoint),
		Changes:     fromChanges(pbPack.Changes),
	}
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

func fromTextNodePos(pbPos *api.TextNodePos) *datatype.TextNodePos {
	return datatype.NewTextNodePos(
		datatype.NewTextNodeID(fromTimeTicket(pbPos.CreatedAt), int(pbPos.Offset)),
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

func fromElement(pbElement *api.JSONElement) datatype.Element {
	switch pbElement.Type {
	case api.ValueType_JSON_OBJECT:
		return json.NewObject(
			datatype.NewRHT(),
			fromTimeTicket(pbElement.CreatedAt),
		)
	case api.ValueType_JSON_ARRAY:
		return json.NewArray(
			datatype.NewRGA(),
			fromTimeTicket(pbElement.CreatedAt),
		)
	case api.ValueType_BOOLEAN:
		return datatype.NewPrimitive(
			datatype.ValueFromBytes(datatype.Boolean, pbElement.Value),
			fromTimeTicket(pbElement.CreatedAt),
		)
	case api.ValueType_INTEGER:
		return datatype.NewPrimitive(
			datatype.ValueFromBytes(datatype.Integer, pbElement.Value),
			fromTimeTicket(pbElement.CreatedAt),
		)
	case api.ValueType_LONG:
		return datatype.NewPrimitive(
			datatype.ValueFromBytes(datatype.Long, pbElement.Value),
			fromTimeTicket(pbElement.CreatedAt),
		)
	case api.ValueType_DOUBLE:
		return datatype.NewPrimitive(
			datatype.ValueFromBytes(datatype.Double, pbElement.Value),
			fromTimeTicket(pbElement.CreatedAt),
		)
	case api.ValueType_STRING:
		return datatype.NewPrimitive(
			datatype.ValueFromBytes(datatype.String, pbElement.Value),
			fromTimeTicket(pbElement.CreatedAt),
		)
	case api.ValueType_BYTES:
		return datatype.NewPrimitive(
			datatype.ValueFromBytes(datatype.Bytes, pbElement.Value),
			fromTimeTicket(pbElement.CreatedAt),
		)
	case api.ValueType_DATE:
		return datatype.NewPrimitive(
			datatype.ValueFromBytes(datatype.Date, pbElement.Value),
			fromTimeTicket(pbElement.CreatedAt),
		)
	case api.ValueType_TEXT:
		return datatype.NewText(
			datatype.NewRGATreeSplit(),
			fromTimeTicket(pbElement.CreatedAt),
		)
	}

	panic("fail to decode element")
}
