package types

import (
	"go.mongodb.org/mongo-driver/bson/primitive"

	"github.com/hackerwins/yorkie/api"
	"github.com/hackerwins/yorkie/api/converter"
	"github.com/hackerwins/yorkie/pkg/document/change"
	"github.com/hackerwins/yorkie/pkg/document/operation"
	"github.com/hackerwins/yorkie/pkg/document/time"
	"github.com/hackerwins/yorkie/pkg/log"
)

type ChangeInfo struct {
	DocID      primitive.ObjectID `bson:"doc_id"`
	ServerSeq  uint64             `bson:"server_seq"`
	ClientSeq  uint32             `bson:"client_seq"`
	Lamport    uint64             `bson:"lamport"`
	Actor      primitive.ObjectID `bson:"actor"`
	Message    string             `bson:"message"`
	Operations [][]byte           `bson:"operations"`
}

func EncodeOperation(operations []operation.Operation) [][]byte {
	var encodedOps [][]byte

	for _, pbOp := range converter.ToOperations(operations) {
		encodedOp, err := pbOp.Marshal()
		if err != nil {
			panic("fail to encode operation")
		}
		encodedOps = append(encodedOps, encodedOp)
	}

	return encodedOps
}

func EncodeActorID(id time.ActorID) primitive.ObjectID {
	objectID := primitive.ObjectID{}
	copy(objectID[:], id[:])
	return objectID
}

func (i *ChangeInfo) ToChange() (*change.Change, error) {
	actorID := time.ActorID{}
	copy(actorID[:], i.Actor[:])
	changeID := change.NewID(i.ClientSeq, i.Lamport, &actorID)

	var pbOps []*api.Operation
	for _, bytesOp := range i.Operations {
		pbOp := api.Operation{}
		if err := pbOp.Unmarshal(bytesOp); err != nil {
			log.Logger.Error(err)
			return nil, err
		}
		pbOps = append(pbOps, &pbOp)
	}

	c := change.New(changeID, i.Message, converter.FromOperations(pbOps))
	c.SetServerSeq(i.ServerSeq)

	return c, nil
}
