package rpc

import (
	"context"
	"github.com/yorkie-team/yorkie/server/rpc/auth"

	"connectrpc.com/connect"

	"github.com/yorkie-team/yorkie/api/converter"
	"github.com/yorkie-team/yorkie/api/types"
	api "github.com/yorkie-team/yorkie/api/yorkie/v1"
	"github.com/yorkie-team/yorkie/pkg/document"
	"github.com/yorkie-team/yorkie/pkg/document/time"
	"github.com/yorkie-team/yorkie/server/backend"
	"github.com/yorkie-team/yorkie/server/clients"
	"github.com/yorkie-team/yorkie/server/documents"
	"github.com/yorkie-team/yorkie/server/logging"
	"github.com/yorkie-team/yorkie/server/packs"
	"github.com/yorkie-team/yorkie/server/projects"
)

type systemServer struct {
	backend    *backend.Backend
	serviceCtx context.Context
}

func newSystemServer(serviceCtx context.Context, be *backend.Backend) *systemServer {
	return &systemServer{
		backend:    be,
		serviceCtx: serviceCtx,
	}
}

// DetachDocument detaches the given document to the client.
func (s *systemServer) DetachDocument(
	ctx context.Context,
	req *connect.Request[api.DetachDocumentRequest],
) (*connect.Response[api.DetachDocumentResponse], error) {
	actorID, err := time.ActorIDFromHex(req.Msg.ClientId)
	if err != nil {
		return nil, err
	}

	pack, err := converter.FromChangePack(req.Msg.ChangePack)
	if err != nil {
		return nil, err
	}
	docID, err := converter.FromDocumentID(req.Msg.DocumentId)
	if err != nil {
		return nil, err
	}

	if err := auth.VerifyAccess(ctx, s.backend, &types.AccessInfo{
		Method: types.DeactivateClient,
	}); err != nil {
		return nil, err
	}

	project := projects.From(ctx)
	locker, err := s.backend.Coordinator.NewLocker(ctx, packs.PushPullKey(project.ID, pack.DocumentKey))
	if err != nil {
		return nil, err
	}

	if err := locker.Lock(ctx); err != nil {
		return nil, err
	}
	defer func() {
		if err := locker.Unlock(ctx); err != nil {
			logging.DefaultLogger().Error(err)
		}
	}()

	clientInfo, err := clients.FindActiveClientInfo(ctx, s.backend.DB, types.ClientRefKey{
		ProjectID: project.ID,
		ClientID:  types.IDFromActorID(actorID),
	})
	if err != nil {
		return nil, err
	}

	docRefKey := types.DocRefKey{
		ProjectID: project.ID,
		DocID:     docID,
	}
	docInfo, err := documents.FindDocInfoByRefKey(ctx, s.backend, docRefKey)
	if err != nil {
		return nil, err
	}

	isAttached, err := documents.IsDocumentAttached(
		ctx, s.backend,
		docRefKey,
		clientInfo.ID,
	)
	if err != nil {
		return nil, err
	}

	var status document.StatusType
	if req.Msg.RemoveIfNotAttached && !isAttached {
		pack.IsRemoved = true
		status = document.StatusRemoved
	} else {
		status = document.StatusDetached
	}

	pulled, err := packs.PushPull(ctx, s.backend, project, clientInfo, docInfo, pack, packs.PushPullOptions{
		Mode:   types.SyncModePushPull,
		Status: status,
	})
	if err != nil {
		return nil, err
	}

	pbChangePack, err := pulled.ToPBChangePack()
	if err != nil {
		return nil, err
	}

	return connect.NewResponse(&api.DetachDocumentResponse{
		ChangePack: pbChangePack,
	}), nil
}
