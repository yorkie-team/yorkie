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
			assert.Equal(t, status.Convert(err).Code(), codes.InvalidArgument)

			_, err = rpcServer.DeactivateClient(
				context.Background(),
				&api.DeactivateClientRequest{ClientId: ""},
			)
			assert.Equal(t, status.Convert(err).Code(), codes.InvalidArgument)

			// client not found
			_, err = rpcServer.DeactivateClient(
				context.Background(),
				&api.DeactivateClientRequest{ClientId: "000000000000000000000000"},
			)
			assert.Equal(t, status.Convert(err).Code(), codes.NotFound)
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
			assert.Equal(t, status.Convert(err).Code(), codes.FailedPrecondition)

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
			assert.Nil(t, err)

			// document not found
			_, err = rpcServer.DetachDocument(
				context.Background(),
				&api.DetachDocumentRequest{
					ClientId: activateResp.ClientId,
					ChangePack: &api.ChangePack{
						DocumentKey: &api.DocumentKey{
							Collection: "invalid-collection", Document: "invalid-document",
						},
						Checkpoint: &api.Checkpoint{ServerSeq: 0, ClientSeq: 0},
					},
				},
			)
			assert.Equal(t, status.Convert(err).Code(), codes.NotFound)

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
			assert.Equal(t, status.Convert(err).Code(), codes.FailedPrecondition)
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

			defer func() {
				_, err := rpcServer.DeactivateClient(
					context.Background(),
					&api.DeactivateClientRequest{ClientId: activateResp.ClientId},
				)
				assert.Nil(t, err)
			}()

			_, err = rpcServer.AttachDocument(
				context.Background(),
				&api.AttachDocumentRequest{
					ClientId:   activateResp.ClientId,
					ChangePack: packWithNoChanges,
				},
			)
			assert.Nil(t, err)

			defer func() {
				_, err = rpcServer.DetachDocument(
					context.Background(),
					&api.DetachDocumentRequest{
						ClientId:   activateResp.ClientId,
						ChangePack: packWithNoChanges,
					},
				)
				assert.Nil(t, err)
			}()

			_, err = rpcServer.PushPull(
				context.Background(),
				&api.PushPullRequest{
					ClientId:   activateResp.ClientId,
					ChangePack: packWithNoChanges,
				},
			)
			assert.Nil(t, err)
		})
	})
}

func withRPCServer(
	t *testing.T,
	f func(t *testing.T, rpcServer *rpc.Server),
) {
	be, err := backend.New(&mongo.Config{
		ConnectionURI:        testhelper.TestMongoConnectionURI,
		YorkieDatabase:       testhelper.TestDBName(),
		ConnectionTimeoutSec: 5,
		PingTimeoutSec:       5,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		err := be.Close()
		assert.Nil(t, err)
	}()

	rpcServer, err := rpc.NewRPCServer(testhelper.TestPort, be)
	if err != nil {
		t.Fatal(err)
	}

	defer func() {
		rpcServer.Shutdown(true)
	}()

	f(t, rpcServer)
}
