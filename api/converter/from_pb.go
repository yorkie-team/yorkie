package converter

import (
	"github.com/hackerwins/rottie/api"
	"github.com/hackerwins/rottie/pkg/document/change"
	"github.com/hackerwins/rottie/pkg/document/checkpoint"
	"github.com/hackerwins/rottie/pkg/document/json"
	"github.com/hackerwins/rottie/pkg/document/json/datatype"
	"github.com/hackerwins/rottie/pkg/document/key"
	"github.com/hackerwins/rottie/pkg/document/operation"
	"github.com/hackerwins/rottie/pkg/document/time"
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
				decoded.Set.Key,
				fromElement(decoded.Set.Value),
				fromTimeTicket(decoded.Set.ParentCreatedAt),
				fromTimeTicket(decoded.Set.ExecutedAt),
			)
		case *api.Operation_Add_:
			op = operation.NewAdd(
				fromElement(decoded.Add.Value),
				fromTimeTicket(decoded.Add.ParentCreatedAt),
				fromTimeTicket(decoded.Add.PrevCreatedAt),
				fromTimeTicket(decoded.Add.ExecutedAt),
			)
		}
		ops = append(ops, op)
	}

	return ops
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
		createdAt := fromTimeTicket(pbElement.CreatedAt)
		return json.NewArray(
			datatype.NewRGA(),
			createdAt,
		)
	case api.ValueType_STRING:
		return datatype.NewPrimitive(
			string(pbElement.Value.GetValue()),
			fromTimeTicket(pbElement.CreatedAt),
		)
	}

	panic("fail to decode element")
}
