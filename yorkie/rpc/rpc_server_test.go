package rpc_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/yorkie-team/yorkie/api"
	"github.com/yorkie-team/yorkie/testhelper"
	"github.com/yorkie-team/yorkie/yorkie/backend"
	"github.com/yorkie-team/yorkie/yorkie/backend/mongo"
	"github.com/yorkie-team/yorkie/yorkie/rpc"
)

func TestRPCServer(t *testing.T) {
	withRPCServer(t, func(t *testing.T, rpcServer *rpc.Server) {
		t.Run("activate/deactivate client test", func(t *testing.T) {
			activateResp, err := rpcServer.ActivateClient(
				context.Background(),
				&api.ActivateClientRequest{ClientKey: t.Name()},
			)
			assert.Nil(t, err)

			_, err = rpcServer.DeactivateClient(
				context.Background(),
				&api.DeactivateClientRequest{ClientId: activateResp.ClientId},
			)
			assert.Nil(t, err)

			// invalid argument
			_, err = rpcServer.ActivateClient(
				context.Background(),
				&api.ActivateClientRequest{ClientKey: ""},
			)
			assert.Equal(t, codes.InvalidArgument, status.Convert(err).Code())

			_, err = rpcServer.DeactivateClient(
				context.Background(),
				&api.DeactivateClientRequest{ClientId: ""},
			)
			assert.Equal(t, codes.InvalidArgument, status.Convert(err).Code())

			// client not found
			_, err = rpcServer.DeactivateClient(
				context.Background(),
				&api.DeactivateClientRequest{ClientId: "000000000000000000000000"},
			)
			assert.Equal(t, codes.NotFound, status.Convert(err).Code())
		})

		t.Run("attach/detach document test", func(t *testing.T) {
			activateResp, err := rpcServer.ActivateClient(
				context.Background(),
				&api.ActivateClientRequest{ClientKey: t.Name()},
			)
			assert.Nil(t, err)

			packWithNoChanges := &api.ChangePack{
				DocumentKey: &api.DocumentKey{
					Collection: t.Name(), Document: t.Name(),
				},
				Checkpoint: &api.Checkpoint{ServerSeq: 0, ClientSeq: 0},
			}

			_, err = rpcServer.AttachDocument(
				context.Background(),
				&api.AttachDocumentRequest{
					ClientId:   activateResp.ClientId,
					ChangePack: packWithNoChanges,
				},
			)
			assert.Nil(t, err)

			// try to attach already attached document
			_, err = rpcServer.AttachDocument(
				context.Background(),
				&api.AttachDocumentRequest{
					ClientId:   activateResp.ClientId,
					ChangePack: packWithNoChanges,
				},
			)
			assert.Equal(t, codes.FailedPrecondition, status.Convert(err).Code())

			_, err = rpcServer.DetachDocument(
				context.Background(),
				&api.DetachDocumentRequest{
					ClientId:   activateResp.ClientId,
					ChangePack: packWithNoChanges,
				},
			)
			assert.Nil(t, err)

			// try to detach already detached document
			_, err = rpcServer.DetachDocument(
				context.Background(),
				&api.DetachDocumentRequest{
					ClientId:   activateResp.ClientId,
					ChangePack: packWithNoChanges,
				},
			)
			assert.Equal(t, codes.FailedPrecondition, status.Convert(err).Code())

			// document not found
			_, err = rpcServer.DetachDocument(
				context.Background(),
				&api.DetachDocumentRequest{
					ClientId: activateResp.ClientId,
					ChangePack: &api.ChangePack{
						DocumentKey: &api.DocumentKey{
							Collection: "invalid", Document: "invalid",
						},
						Checkpoint: &api.Checkpoint{ServerSeq: 0, ClientSeq: 0},
					},
				},
			)
			assert.Equal(t, codes.NotFound, status.Convert(err).Code())

			_, err = rpcServer.DeactivateClient(
				context.Background(),
				&api.DeactivateClientRequest{ClientId: activateResp.ClientId},
			)
			assert.Nil(t, err)

			// try to attach the document with a deactivated client
			_, err = rpcServer.AttachDocument(
				context.Background(),
				&api.AttachDocumentRequest{
					ClientId:   activateResp.ClientId,
					ChangePack: packWithNoChanges,
				},
			)
			assert.Equal(t, codes.FailedPrecondition, status.Convert(err).Code())
		})

		t.Run("push/pull changes test", func(t *testing.T) {
			packWithNoChanges := &api.ChangePack{
				DocumentKey: &api.DocumentKey{
					Collection: t.Name(), Document: t.Name(),
				},
				Checkpoint: &api.Checkpoint{ServerSeq: 0, ClientSeq: 0},
			}

			activateResp, err := rpcServer.ActivateClient(
				context.Background(),
				&api.ActivateClientRequest{ClientKey: t.Name()},
			)
			assert.Nil(t, err)

			_, err = rpcServer.AttachDocument(
				context.Background(),
				&api.AttachDocumentRequest{
					ClientId:   activateResp.ClientId,
					ChangePack: packWithNoChanges,
				},
			)
			assert.Nil(t, err)

			_, err = rpcServer.PushPull(
				context.Background(),
				&api.PushPullRequest{
					ClientId:   activateResp.ClientId,
					ChangePack: packWithNoChanges,
				},
			)
			assert.Nil(t, err)

			_, err = rpcServer.DetachDocument(
				context.Background(),
				&api.DetachDocumentRequest{
					ClientId:   activateResp.ClientId,
					ChangePack: packWithNoChanges,
				},
			)
			assert.Nil(t, err)

			// try to push/pull with detached document
			_, err = rpcServer.PushPull(
				context.Background(),
				&api.PushPullRequest{
					ClientId:   activateResp.ClientId,
					ChangePack: packWithNoChanges,
				},
			)
			assert.Equal(t, codes.FailedPrecondition, status.Convert(err).Code())

			_, err = rpcServer.DeactivateClient(
				context.Background(),
				&api.DeactivateClientRequest{ClientId: activateResp.ClientId},
			)
			assert.Nil(t, err)

			// try to push/pull with deactivated client
			_, err = rpcServer.PushPull(
				context.Background(),
				&api.PushPullRequest{
					ClientId:   activateResp.ClientId,
					ChangePack: packWithNoChanges,
				},
			)
			assert.Equal(t, codes.FailedPrecondition, status.Convert(err).Code())
		})
	})
}

func withRPCServer(
	t *testing.T,
	f func(t *testing.T, rpcServer *rpc.Server),
) {
	be, err := backend.New(&backend.Config{
		SnapshotThreshold: testhelper.SnapshotThreshold,
	}, &mongo.Config{
		ConnectionURI:        testhelper.MongoConnectionURI,
		YorkieDatabase:       testhelper.TestDBName(),
		ConnectionTimeoutSec: testhelper.MongoConnectionTimeoutSec,
		PingTimeoutSec:       testhelper.MongoPingTimeoutSec,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		err := be.Close()
		assert.Nil(t, err)
	}()

	rpcServer, err := rpc.NewServer(&rpc.Config{
		Port: testhelper.RPCPort,
	}, be)
	if err != nil {
		t.Fatal(err)
	}

	defer func() {
		rpcServer.Shutdown(true)
	}()

	f(t, rpcServer)
}
