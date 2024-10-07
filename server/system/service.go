package system

import (
	"context"

	"github.com/yorkie-team/yorkie/api/converter"
	"github.com/yorkie-team/yorkie/api/types"
	"github.com/yorkie-team/yorkie/pkg/document"
	"github.com/yorkie-team/yorkie/pkg/document/json"
	"github.com/yorkie-team/yorkie/pkg/document/presence"
	"github.com/yorkie-team/yorkie/server/backend"
	"github.com/yorkie-team/yorkie/server/backend/database"
	"github.com/yorkie-team/yorkie/server/documents"
	"github.com/yorkie-team/yorkie/server/logging"
	"github.com/yorkie-team/yorkie/server/packs"
)

type systemService struct {
	be  *backend.Backend
	ctx context.Context
}

func newSystemService(ctx context.Context, be *backend.Backend) *systemService {
	return &systemService{
		be:  be,
		ctx: ctx,
	}
}

func (s *systemService) DetachDocument(
	docID string,
	clientInfo database.ClientInfo,
	info database.ClientDocInfo,
	project types.Project,
) (string, error) {
	docInfo, err := s.be.DB.FindDocInfoByRefKey(s.ctx, types.DocRefKey{
		ProjectID: clientInfo.ProjectID,
		DocID:     types.ID(docID),
	})
	if err != nil {
		return "", err
	}

	actorID, err := clientInfo.ID.ToActorID()
	if err != nil {
		return "", err
	}

	doc, err := packs.BuildDocForCheckpoint(s.ctx, s.be, docInfo, info.ServerSeq, info.ClientSeq, actorID)
	if err != nil {
		return "", err
	}

	if err := doc.Update(func(root *json.Object, p *presence.Presence) error {
		p.Clear()
		return nil
	}); err != nil {
		return "", err
	}

	pack := doc.CreateChangePack()
	// TODO(raararaara): verifyAccess

	locker, err := s.be.Coordinator.NewLocker(s.ctx, packs.PushPullKey(clientInfo.ProjectID, pack.DocumentKey))
	if err != nil {
		return "", err
	}

	if err := locker.Lock(s.ctx); err != nil {
		return "", err
	}
	defer func() {
		if err := locker.Unlock(s.ctx); err != nil {
			logging.DefaultLogger().Error(err)
		}
	}()

	docRefKey := types.DocRefKey{
		ProjectID: clientInfo.ProjectID,
		DocID:     types.ID(docID),
	}
	isAttached, err := documents.IsDocumentAttached(
		s.ctx, s.be,
		docRefKey,
		clientInfo.ID,
	)
	if err != nil {
		return "", err
	}

	var status document.StatusType
	if !isAttached {
		status = document.StatusRemoved
	} else {
		status = document.StatusDetached
	}

	pulled, err := packs.PushPull(s.ctx, s.be, &project, &clientInfo, docInfo, pack, packs.PushPullOptions{
		Mode:   types.SyncModePushPull,
		Status: status,
	})
	if err != nil {
		return "", err
	}
	pbChangePack, err := pulled.ToPBChangePack()
	if err != nil {
		return "", err
	}
	respPack, err := converter.FromChangePack(pbChangePack)
	if err != nil {
		return "", err
	}

	if err := doc.ApplyChangePack(respPack); err != nil {
		return "", err
	}
	if doc.Status() != document.StatusRemoved {
		doc.SetStatus(document.StatusDetached)
	}

	return doc.Key().String(), nil
}
