package packs

import (
	"context"

	"github.com/hackerwins/yorkie/pkg/document/change"
	"github.com/hackerwins/yorkie/pkg/document/checkpoint"
	"github.com/hackerwins/yorkie/pkg/document/key"
	"github.com/hackerwins/yorkie/pkg/log"
	"github.com/hackerwins/yorkie/yorkie/backend"
	"github.com/hackerwins/yorkie/yorkie/types"
)

func PushPull(
	ctx context.Context,
	be *backend.Backend,
	clientInfo *types.ClientInfo,
	docInfo *types.DocInfo,
	pack *change.Pack,
) (*change.Pack, error) {
	// TODO Changes may be reordered or missing during communication on the network.
	// We should check the change.pack with checkpoint to make sure the changes are in the correct order.

	initialServerSeq := docInfo.ServerSeq

	// 01. push changes
	pushedCP, pushedChanges, err := pushChanges(clientInfo, docInfo, pack, initialServerSeq)
	if err != nil {
		return nil, err
	}

	// 02. pull changes
	pulledCP, pulledChanges, err := pullChanges(ctx, be, clientInfo, docInfo, pack, pushedCP, initialServerSeq)
	if err != nil {
		return nil, err
	}

	if err := clientInfo.UpdateCheckpoint(docInfo.ID, pulledCP); err != nil {
		return nil, err
	}

	// 03. save into MongoDB
	if err := be.Mongo.CreateChangeInfos(ctx, docInfo.ID, pushedChanges); err != nil {
		return nil, err
	}

	if err := be.Mongo.UpdateDocInfo(ctx, clientInfo, docInfo); err != nil {
		return nil, err
	}

	if err := be.Mongo.UpdateClientInfoAfterPushPull(ctx, clientInfo, docInfo); err != nil {
		return nil, err
	}

	docKey, err := key.FromBSONKey(docInfo.Key)
	if err != nil {
		return nil, err
	}

	return change.NewPack(
		docKey,
		pulledCP,
		pulledChanges,
	), nil
}

func pushChanges(
	clientInfo *types.ClientInfo,
	docInfo *types.DocInfo,
	pack *change.Pack,
	initialServerSeq uint64,
) (*checkpoint.Checkpoint, []*change.Change, error) {
	cp := clientInfo.GetCheckpoint(docInfo.ID)

	var pushedChanges []*change.Change
	for _, c := range pack.Changes {
		if c.ID().ClientSeq() > cp.ClientSeq {
			serverSeq := docInfo.IncreaseServerSeq()
			cp = cp.NextServerSeq(serverSeq)
			c.SetServerSeq(serverSeq)
			pushedChanges = append(pushedChanges, c)
		} else {
			log.Logger.Warnf("change is rejected: %v", c)
		}

		cp = cp.SyncClientSeq(c.ClientSeq())
	}

	if len(pack.Changes) > 0 {
		log.Logger.Infof(
			"PUSH: '%s' pushes %d changes into '%s', rejected %d changes, serverSeq: %d -> %d, cp: %s",
			clientInfo.ID.Hex(),
			len(pushedChanges),
			docInfo.Key,
			len(pack.Changes)-len(pushedChanges),
			initialServerSeq,
			docInfo.ServerSeq,
			cp.String(),
		)
	}

	return cp, pushedChanges, nil
}

func pullChanges(
	ctx context.Context,
	be *backend.Backend,
	clientInfo *types.ClientInfo,
	docInfo *types.DocInfo,
	pack *change.Pack,
	pushedCP *checkpoint.Checkpoint,
	initialServerSeq uint64,
) (*checkpoint.Checkpoint, []*change.Change, error) {
	pulledChanges, err := be.Mongo.FindChangeInfosBetweenServerSeqs(
		ctx,
		docInfo.ID,
		pack.Checkpoint.ServerSeq+1,
		initialServerSeq,
	)
	if err != nil {
		return nil, nil, err
	}

	pulledCP := pushedCP.NextServerSeq(docInfo.ServerSeq)

	if len(pulledChanges) > 0 {
		log.Logger.Infof(
			"PULL: '%s' pulls %d changes(%d~%d) from '%s', cp: %s",
			clientInfo.ID.Hex(),
			len(pulledChanges),
			pulledChanges[0].ServerSeq(),
			pulledChanges[len(pulledChanges)-1].ServerSeq(),
			docInfo.Key,
			pulledCP.String(),
		)
	}

	return pulledCP, pulledChanges, nil
}
