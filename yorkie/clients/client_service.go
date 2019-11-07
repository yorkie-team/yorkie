package clients

import (
	"context"

	"github.com/hackerwins/yorkie/pkg/document/change"
	"github.com/hackerwins/yorkie/yorkie/backend"
	"github.com/hackerwins/yorkie/yorkie/types"
)

func Activate(
	ctx context.Context,
	be *backend.Backend,
	clientKey string,
) (*types.ClientInfo, error) {
	return be.Mongo.ActivateClient(ctx, clientKey)
}

func Deactivate(
	ctx context.Context,
	be *backend.Backend,
	clientID string,
) (*types.ClientInfo, error) {
	return be.Mongo.DeactivateClient(ctx, clientID)
}

func AttachDocument(
	ctx context.Context,
	be *backend.Backend,
	clientID string,
	pack *change.Pack,
) (*types.ClientInfo, *types.DocInfo, error) {
	clientInfo, err := be.Mongo.FindClientInfoByID(ctx, clientID)
	if err != nil {
		return nil, nil, err
	}

	docInfo, err := be.Mongo.FindDocInfoByKey(ctx, clientInfo, pack.DocumentKey.BSONKey())
	if err != nil {
		return nil, nil, err
	}

	if err := clientInfo.AttachDocument(docInfo.ID, pack.Checkpoint); err != nil {
		return nil, nil, err
	}

	return clientInfo, docInfo, nil
}

func DetachDocument(
	ctx context.Context,
	be *backend.Backend,
	clientID string,
	pack *change.Pack,
) (*types.ClientInfo, *types.DocInfo, error) {
	clientInfo, err := be.Mongo.FindClientInfoByID(ctx, clientID)
	if err != nil {
		return nil, nil, err
	}

	docInfo, err := be.Mongo.FindDocInfoByKey(ctx, clientInfo, pack.DocumentKey.BSONKey())
	if err != nil {
		return nil, nil, err
	}

	if err := clientInfo.DetachDocument(docInfo.ID, pack.Checkpoint); err != nil {
		return nil, nil, err
	}

	return clientInfo, docInfo, nil
}

func PushPullDocument(
	ctx context.Context,
	be *backend.Backend,
	clientID string,
	pack *change.Pack,
) (*types.ClientInfo, *types.DocInfo, error) {
	clientInfo, err := be.Mongo.FindClientInfoByID(ctx, clientID)
	if err != nil {
		return nil, nil, err
	}

	docInfo, err := be.Mongo.FindDocInfoByKey(ctx, clientInfo, pack.DocumentKey.BSONKey())
	if err != nil {
		return nil, nil, err
	}

	if err := clientInfo.CheckDocumentAttached(docInfo.ID.Hex()); err != nil {
		return nil, nil, err
	}

	return clientInfo, docInfo, nil
}
