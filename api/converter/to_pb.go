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

func ToChangePack(pack *change.Pack) *api.ChangePack {
	return &api.ChangePack{
		DocumentKey: toDocumentKey(pack.DocumentKey),
		Checkpoint:  toCheckpoint(pack.Checkpoint),
		Changes:     toChanges(pack.Changes),
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
					Value:           toJSONElement(op.Value()),
					ExecutedAt:      toTimeTicket(op.ExecutedAt()),
				},
			}
		case *operation.Add:
			pbOperation.Body = &api.Operation_Add_{
				Add: &api.Operation_Add{
					ParentCreatedAt: toTimeTicket(op.ParentCreatedAt()),
					PrevCreatedAt:   toTimeTicket(op.PrevCreatedAt()),
					Value:           toJSONElement(op.Value()),
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
					ParentCreatedAt: toTimeTicket(op.ParentCreatedAt()),
					Content:         op.Content(),
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

func toJSONElement(elem datatype.Element) *api.JSONElement {
	switch elem := elem.(type) {
	case *json.Object:
		return &api.JSONElement{
			Type:      api.ValueType_JSON_OBJECT,
			CreatedAt: toTimeTicket(elem.CreatedAt()),
		}
	case *json.Array:
		return &api.JSONElement{
			Type:      api.ValueType_JSON_ARRAY,
			CreatedAt: toTimeTicket(elem.CreatedAt()),
		}
	case *datatype.Primitive:
		switch elem.ValueType() {
		case datatype.Boolean:
			return &api.JSONElement{
				Type:      api.ValueType_BOOLEAN,
				CreatedAt: toTimeTicket(elem.CreatedAt()),
				Value:     elem.Bytes(),
			}
		case datatype.Integer:
			return &api.JSONElement{
				Type:      api.ValueType_INTEGER,
				CreatedAt: toTimeTicket(elem.CreatedAt()),
				Value:     elem.Bytes(),
			}
		case datatype.Long:
			return &api.JSONElement{
				Type:      api.ValueType_LONG,
				CreatedAt: toTimeTicket(elem.CreatedAt()),
				Value:     elem.Bytes(),
			}
		case datatype.Double:
			return &api.JSONElement{
				Type:      api.ValueType_DOUBLE,
				CreatedAt: toTimeTicket(elem.CreatedAt()),
				Value:     elem.Bytes(),
			}
		case datatype.String:
			return &api.JSONElement{
				Type:      api.ValueType_STRING,
				CreatedAt: toTimeTicket(elem.CreatedAt()),
				Value:     elem.Bytes(),
			}
		case datatype.Bytes:
			return &api.JSONElement{
				Type:      api.ValueType_BYTES,
				CreatedAt: toTimeTicket(elem.CreatedAt()),
				Value:     elem.Bytes(),
			}
		case datatype.Date:
			return &api.JSONElement{
				Type:      api.ValueType_DATE,
				CreatedAt: toTimeTicket(elem.CreatedAt()),
				Value:     elem.Bytes(),
			}
		}
	case *datatype.Text:
		return &api.JSONElement{
			Type:      api.ValueType_TEXT,
			CreatedAt: toTimeTicket(elem.CreatedAt()),
		}
	}
	panic("fail to encode JSONElement to protobuf")
}

func toTimeTicket(ticket *time.Ticket) *api.TimeTicket {
	return &api.TimeTicket{
		Lamport:   ticket.Lamport(),
		Delimiter: ticket.Delimiter(),
		ActorId:   ticket.ActorID().String(),
	}
}
