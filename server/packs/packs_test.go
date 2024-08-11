package packs_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"golang.org/x/net/context"

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
)

var (
	clientID = "000000000000000000000001"
)

func Test(t *testing.T) {
	t.Run("push/pull sequential ClientSeq test (happy case)", func(t *testing.T) {
		RunPushPullWithSequentialClientSeqTest(t)
	})

	t.Run("push/pull not sequential ClientSeq with DocInfo.Checkpoint.ClientSeq test", func(t *testing.T) {
		RunPushPullWithNotSequentialClientSeqWithCheckpoint(t)
	})

	t.Run("push/pull not sequential ClientSeq in changes test", func(t *testing.T) {
		RunPushPullWithNotSequentialClientSeqInChangesTest(t)
	})

	t.Run("push/pull ClientSeq less than ClientInfo's ClientSeq (duplicated request)", func(t *testing.T) {
		RunPushPullWithClientSeqLessThanClientInfoTest(t)
	})

	t.Run("push/pull ServerSeq greater than DocInfo's ServerSeq", func(t *testing.T) {
		RunPushPullWithServerSeqGreaterThanDocInfoTest(t)
	})
}

func RunPushPullWithSequentialClientSeqTest(t *testing.T) {
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

func RunPushPullWithNotSequentialClientSeqInChangesTest(t *testing.T) {
	ctx := context.Background()
	be := setUpBackend(t)
	project, _ := be.DB.FindProjectInfoByID(
		ctx,
		database.DefaultProjectID,
	)

	clientInfo, _ := clients.Activate(ctx, be.DB, project.ToProject(), clientID)

	actorID, _ := time.ActorIDFromHex(clientID)
	changePackWithNotSequentialClientSeq, _ :=
		createChangePackWithNotSequentialClientSeqInChanges(helper.TestDocKey(t).String(), actorID.Bytes())

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
	assert.Equal(t, connecthelper.CodeOf(packs.ErrClientSeqInChangesAreNotSequential), connecthelper.CodeOf(err))
}

func RunPushPullWithNotSequentialClientSeqWithCheckpoint(t *testing.T) {
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

	changePackWithNotSequentialClientSeqWithCheckpoint, _ :=
		createChangePackWithNotSequentialClientSeqWithCheckpoint(helper.TestDocKey(t).String(), actorID.Bytes())
	_, err = packs.PushPull(ctx, be, project.ToProject(), clientInfo, docInfo,
		changePackWithNotSequentialClientSeqWithCheckpoint, packs.PushPullOptions{
			Mode:   types.SyncModePushPull,
			Status: document.StatusAttached,
		})
	assert.Equal(t, connecthelper.CodeOf(packs.ErrClientSeqNotSequentialWithCheckpoint), connecthelper.CodeOf(err))
}

func RunPushPullWithClientSeqLessThanClientInfoTest(t *testing.T) {
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

	changePackWithClientSeqLessThanClientInfo, _ :=
		createChangePackWithClientSeqLessThanClientInfo(helper.TestDocKey(t).String(), actorID.Bytes())
	_, err = packs.PushPull(ctx, be, project.ToProject(), clientInfo, docInfo,
		changePackWithClientSeqLessThanClientInfo, packs.PushPullOptions{
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
		Checkpoint:  &api.Checkpoint{ServerSeq: 0, ClientSeq: 3},
		Changes: []*api.Change{
			createChange(1, 1, actorID),
			createChange(2, 2, actorID),
			createChange(3, 3, actorID),
		},
	})
}

func createChangePackWithNotSequentialClientSeqInChanges(documentKey string, actorID []byte) (*change.Pack, error) {
	return converter.FromChangePack(&api.ChangePack{
		DocumentKey: documentKey,
		Checkpoint:  &api.Checkpoint{ServerSeq: 0, ClientSeq: 3},
		Changes: []*api.Change{
			createChange(1, 1, actorID),
			createChange(3, 3, actorID),
			createChange(2, 2, actorID),
		},
	})
}

func createChangePackWithNotSequentialClientSeqWithCheckpoint(
	documentKey string, actorID []byte) (*change.Pack, error) {
	return converter.FromChangePack(&api.ChangePack{
		DocumentKey: documentKey,
		Checkpoint:  &api.Checkpoint{ServerSeq: 3, ClientSeq: 1e9},
		Changes: []*api.Change{
			createChange(1e9, 1e9, actorID),
		},
	})
}

func createChangePackWithClientSeqLessThanClientInfo(documentKey string, actorID []byte) (*change.Pack, error) {
	return converter.FromChangePack(&api.ChangePack{
		DocumentKey: documentKey,
		Checkpoint:  &api.Checkpoint{ServerSeq: 3, ClientSeq: 3},
		Changes: []*api.Change{
			createChange(0, 0, actorID),
		},
	})
}

func createChangePackFixture(documentKey string, actorID []byte) (*change.Pack, error) {
	return createChangePackWithSequentialClientSeq(documentKey, actorID)
}

func createChangePackWithServerSeqGreaterThanDocInfo(documentKey string) (*change.Pack, error) {
	return converter.FromChangePack(&api.ChangePack{
		DocumentKey: documentKey,
		Checkpoint:  &api.Checkpoint{ServerSeq: 1e9, ClientSeq: 3},
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
