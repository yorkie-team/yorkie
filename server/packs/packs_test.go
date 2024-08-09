package packs_test

import (
	"context"
	"github.com/stretchr/testify/assert"
	"github.com/yorkie-team/yorkie/api/converter"
	"github.com/yorkie-team/yorkie/api/types"
	api "github.com/yorkie-team/yorkie/api/yorkie/v1"
	"github.com/yorkie-team/yorkie/pkg/document"
	"github.com/yorkie-team/yorkie/pkg/document/change"
	"github.com/yorkie-team/yorkie/pkg/document/time"
	"github.com/yorkie-team/yorkie/server/backend"
	"github.com/yorkie-team/yorkie/server/backend/database"
	"github.com/yorkie-team/yorkie/server/clients"
	"github.com/yorkie-team/yorkie/server/documents"
	"github.com/yorkie-team/yorkie/server/packs"
	"github.com/yorkie-team/yorkie/server/profiling/prometheus"
	"github.com/yorkie-team/yorkie/server/rpc/connecthelper"
	"github.com/yorkie-team/yorkie/test/helper"
	"testing"
)

var (
	clientID = "000000000000000000000001"
)

func Test(t *testing.T) {
	t.Run("test", func(t *testing.T) {
		RunPushPullWithSequentialClientSeqTest(t)
	})

	t.Run("test", func(t *testing.T) {
		RunPushPullWithNotSequentialClientSeqTest(t)
	})

	t.Run("test", func(t *testing.T) {
		RunPushPullWithClientSeqGreaterThanClientInfoTest(t)
	})

	t.Run("test", func(t *testing.T) {
		RunPushPullWithServerSeqGreaterThanDocInfoTest(t)
	})
}

func RunPushPullWithSequentialClientSeqTest(t *testing.T) {
	// given
	ctx := context.Background()
	be := setUpBackend(t)
	project, _ := be.DB.FindProjectInfoByID(
		ctx,
		database.DefaultProjectID,
	)

	clientInfo, _ := clients.Activate(ctx, be.DB, project.ToProject(), clientID)
	actorID, _ := time.ActorIDFromHex(clientID)

	changePackWithSequentialClientSeq, _ :=
		createChangePackWithSequentialClientSeq(helper.TestDocKey(t).String(), actorID.Bytes())

	docInfo, _ := documents.FindDocInfoByKeyAndOwner(
		ctx, be, clientInfo, changePackWithSequentialClientSeq.DocumentKey, true)
	err := clientInfo.AttachDocument(
		docInfo.ID, changePackWithSequentialClientSeq.IsAttached())
	if err != nil {
		assert.Fail(t, "failed to attach document")
	}

	_, err = packs.PushPull(
		ctx, be, project.ToProject(), clientInfo, docInfo,
		changePackWithSequentialClientSeq, packs.PushPullOptions{
			Mode:   types.SyncModePushPull,
			Status: document.StatusAttached,
		})
	assert.NoError(t, err)
}

func RunPushPullWithNotSequentialClientSeqTest(t *testing.T) {
	ctx := context.Background()
	be := setUpBackend(t)
	project, _ := be.DB.FindProjectInfoByID(
		ctx,
		database.DefaultProjectID,
	)

	clientInfo, _ := clients.Activate(ctx, be.DB, project.ToProject(), clientID)

	actorID, _ := time.ActorIDFromHex(clientID)
	changePackWithNotSequentialClientSeq, _ :=
		createChangePackWithNotSequentialClientSeq(helper.TestDocKey(t).String(), actorID.Bytes())

	docInfo, _ := documents.FindDocInfoByKeyAndOwner(ctx, be, clientInfo,
		changePackWithNotSequentialClientSeq.DocumentKey, true)
	err := clientInfo.AttachDocument(
		docInfo.ID, changePackWithNotSequentialClientSeq.IsAttached())
	if err != nil {
		assert.Fail(t, "failed to attach document")
	}

	_, err = packs.PushPull(
		ctx, be, project.ToProject(), clientInfo, docInfo,
		changePackWithNotSequentialClientSeq, packs.PushPullOptions{
			Mode:   types.SyncModePushPull,
			Status: document.StatusAttached,
		})
	assert.Equal(t, connecthelper.CodeOf(packs.ErrClientSeqNotSequential), connecthelper.CodeOf(err))
}

func RunPushPullWithClientSeqGreaterThanClientInfoTest(t *testing.T) {
	ctx := context.Background()
	be := setUpBackend(t)
	project, _ := be.DB.FindProjectInfoByID(
		ctx,
		database.DefaultProjectID,
	)

	clientInfo, _ := clients.Activate(ctx, be.DB, project.ToProject(), clientID)

	actorID, _ := time.ActorIDFromHex(clientID)
	changePackFixture, _ :=
		createChangePackFixture(helper.TestDocKey(t).String(), actorID.Bytes())

	docInfo, _ := documents.FindDocInfoByKeyAndOwner(
		ctx, be, clientInfo, changePackFixture.DocumentKey, true)
	err := clientInfo.AttachDocument(docInfo.ID, changePackFixture.IsAttached())
	if err != nil {
		assert.Fail(t, "failed to attach document")
	}

	_, err = packs.PushPull(ctx, be, project.ToProject(),
		clientInfo, docInfo, changePackFixture, packs.PushPullOptions{
			Mode:   types.SyncModePushPull,
			Status: document.StatusAttached,
		})
	if err != nil {
		assert.Fail(t, "failed to push pull")
	}

	changePackWithClientSeqGreaterThanClientInfo, _ :=
		createChangePackWithClientSeqGreaterThanClientInfo(helper.TestDocKey(t).String(), actorID.Bytes())
	_, err = packs.PushPull(ctx, be, project.ToProject(), clientInfo, docInfo,
		changePackWithClientSeqGreaterThanClientInfo, packs.PushPullOptions{
			Mode:   types.SyncModePushPull,
			Status: document.StatusAttached,
		})

	assert.NoError(t, err)
}

func RunPushPullWithServerSeqGreaterThanDocInfoTest(t *testing.T) {
	ctx := context.Background()
	be := setUpBackend(t)
	project, _ := be.DB.FindProjectInfoByID(
		ctx,
		database.DefaultProjectID,
	)

	clientInfo, _ := clients.Activate(ctx, be.DB, project.ToProject(), clientID)

	actorID, _ := time.ActorIDFromHex(clientID)
	changePackFixture, _ :=
		createChangePackFixture(helper.TestDocKey(t).String(), actorID.Bytes())

	docInfo, _ := documents.FindDocInfoByKeyAndOwner(
		ctx, be, clientInfo, changePackFixture.DocumentKey, true)
	err := clientInfo.AttachDocument(docInfo.ID, changePackFixture.IsAttached())
	if err != nil {
		assert.Fail(t, "failed to attach document")
	}

	_, _ = packs.PushPull(ctx, be, project.ToProject(), clientInfo, docInfo,
		changePackFixture, packs.PushPullOptions{
			Mode:   types.SyncModePushPull,
			Status: document.StatusAttached,
		})

	changePackWithServerSeqGreaterThanDocInfo, _ :=
		createChangePackWithServerSeqGreaterThanDocInfo(helper.TestDocKey(t).String())

	_, err = packs.PushPull(ctx, be, project.ToProject(),
		clientInfo, docInfo, changePackWithServerSeqGreaterThanDocInfo, packs.PushPullOptions{
			Mode:   types.SyncModePushPull,
			Status: document.StatusAttached,
		})

	assert.Equal(t, connecthelper.CodeOf(packs.ErrInvalidServerSeq), connecthelper.CodeOf(err))
}

func createChangePackWithSequentialClientSeq(documentKey string, actorID []byte) (*change.Pack, error) {
	return converter.FromChangePack(&api.ChangePack{
		DocumentKey: documentKey,
		Checkpoint:  &api.Checkpoint{ServerSeq: 0, ClientSeq: 0},
		Changes: []*api.Change{
			createChange(0, 0, actorID),
			createChange(1, 1, actorID),
			createChange(2, 2, actorID),
		},
	})
}

func createChangePackWithNotSequentialClientSeq(documentKey string, actorID []byte) (*change.Pack, error) {
	return converter.FromChangePack(&api.ChangePack{
		DocumentKey: documentKey,
		Checkpoint:  &api.Checkpoint{ServerSeq: 0, ClientSeq: 0},
		Changes: []*api.Change{
			createChange(2, 2, actorID),
			createChange(1, 1, actorID),
			createChange(0, 0, actorID),
		},
	})
}

func createChangePackWithClientSeqGreaterThanClientInfo(documentKey string, actorID []byte) (*change.Pack, error) {
	return converter.FromChangePack(&api.ChangePack{
		DocumentKey: documentKey,
		Checkpoint:  &api.Checkpoint{ServerSeq: 2, ClientSeq: 1e9},
		Changes: []*api.Change{
			createChange(1e9, 1e9, actorID),
		},
	})
}

func createChangePackFixture(documentKey string, actorID []byte) (*change.Pack, error) {
	return createChangePackWithSequentialClientSeq(documentKey, actorID)
}

func createChangePackWithServerSeqGreaterThanDocInfo(documentKey string) (*change.Pack, error) {
	return converter.FromChangePack(&api.ChangePack{
		DocumentKey: documentKey,
		Checkpoint:  &api.Checkpoint{ServerSeq: 1e9, ClientSeq: 2},
	})
}

func createChange(clientSeq uint32, lamport int64, actorID []byte) *api.Change {
	return &api.Change{
		Id: &api.ChangeID{
			ClientSeq: clientSeq,
			Lamport:   lamport,
			ActorId:   actorID,
		},
	}
}

func setUpBackend(
	t *testing.T,
) *backend.Backend {
	conf := helper.TestConfig()

	metrics, err := prometheus.NewMetrics()
	assert.NoError(t, err)

	be, err := backend.New(
		conf.Backend,
		conf.Mongo,
		conf.Housekeeping,
		metrics,
	)
	assert.NoError(t, err)

	return be
}
