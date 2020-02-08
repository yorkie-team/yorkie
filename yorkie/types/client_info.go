package types

import (
	"errors"
	"time"

	"go.mongodb.org/mongo-driver/bson/primitive"

	"github.com/yorkie-team/yorkie/pkg/document/checkpoint"
	"github.com/yorkie-team/yorkie/pkg/log"
)

var (
	ErrClientNotActivated      = errors.New("client not activated")
	ErrDocumentNotAttached     = errors.New("document not attached")
	ErrDocumentAlreadyAttached = errors.New("document already attached")
)

const (
	ClientDeactivated = "deactivated"
	ClientActivated   = "activated"
)

const (
	DocumentAttached = "attached"
	DocumentDetached = "detached"
)

type ClientDocInfo struct {
	Status    string `bson:"status"`
	ServerSeq uint64 `bson:"server_seq"`
	ClientSeq uint32 `bson:"client_seq"`
}

type ClientInfo struct {
	ID        primitive.ObjectID        `bson:"_id"`
	Key       string                    `bson:"key"`
	Status    string                    `bson:"status"`
	Documents map[string]*ClientDocInfo `bson:"documents"`
	CreatedAt time.Time                 `bson:"created_at"`
	UpdatedAt time.Time                 `bson:"updated_at"`
}

func (i *ClientInfo) AttachDocument(docID primitive.ObjectID, cp *checkpoint.Checkpoint) error {
	if i.Status != ClientActivated {
		log.Logger.Error(ErrClientNotActivated)
		return ErrClientNotActivated
	}

	if i.Documents == nil {
		i.Documents = make(map[string]*ClientDocInfo)
	}

	hexDocID := docID.Hex()

	if _, ok := i.Documents[hexDocID]; ok {
		log.Logger.Error(ErrDocumentAlreadyAttached)
		return ErrDocumentAlreadyAttached
	}

	i.Documents[hexDocID] = &ClientDocInfo{
		Status:    DocumentAttached,
		ServerSeq: 0,
		ClientSeq: 0,
	}
	i.UpdatedAt = time.Now()

	return nil
}

func (i *ClientInfo) DetachDocument(docID primitive.ObjectID, cp *checkpoint.Checkpoint) error {
	hexDocID := docID.Hex()
	if err := i.CheckDocumentAttached(hexDocID); err != nil {
		return err
	}

	i.Documents[hexDocID].Status = DocumentDetached
	i.UpdatedAt = time.Now()

	return nil
}

func (i *ClientInfo) GetCheckpoint(id primitive.ObjectID) *checkpoint.Checkpoint {
	clientDocInfo := i.Documents[id.Hex()]
	if clientDocInfo == nil {
		return checkpoint.Initial
	}

	return checkpoint.New(clientDocInfo.ServerSeq, clientDocInfo.ClientSeq)
}

func (i *ClientInfo) UpdateCheckpoint(docID primitive.ObjectID, cp *checkpoint.Checkpoint) error {
	hexDocID := docID.Hex()
	if err := i.CheckDocumentAttached(hexDocID); err != nil {
		return err
	}

	i.Documents[hexDocID].ServerSeq = cp.ServerSeq
	i.Documents[hexDocID].ClientSeq = cp.ClientSeq
	i.UpdatedAt = time.Now()

	return nil
}

func (i *ClientInfo) CheckDocumentAttached(hexDocID string) error {
	if i.Status != ClientActivated {
		log.Logger.Error(ErrClientNotActivated)
		return ErrClientNotActivated
	}

	if i.Documents == nil ||
		i.Documents[hexDocID] == nil ||
		i.Documents[hexDocID].Status == DocumentDetached {
		log.Logger.Error(ErrDocumentNotAttached)
		return ErrDocumentNotAttached
	}

	return nil
}
