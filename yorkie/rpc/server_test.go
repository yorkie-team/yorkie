package rpc_test

import (
	"context"
	"log"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/yorkie-team/yorkie/api"
	"github.com/yorkie-team/yorkie/test/helper"
	"github.com/yorkie-team/yorkie/yorkie/backend"
	"github.com/yorkie-team/yorkie/yorkie/backend/db/mongo"
	"github.com/yorkie-team/yorkie/yorkie/rpc"
)

const (
	nilClientID = "000000000000000000000000"
	// to avoid conflict with rpc port used for client test
	testRPCPort = helper.RPCPort + 100
)

var (
	testRPCServer *rpc.Server

	invalidChangePack = &api.ChangePack{
		DocumentKey: &api.DocumentKey{
			Collection: "invalid", Document: "invalid",
		},
		Checkpoint: nil,
	}
)

func TestMain(m *testing.M) {
	be, err := backend.New(&backend.Config{
		SnapshotThreshold: helper.SnapshotThreshold,
	}, &mongo.Config{
		ConnectionURI:        helper.MongoConnectionURI,
		YorkieDatabase:       helper.TestDBName(),
		ConnectionTimeoutSec: helper.MongoConnectionTimeoutSec,
		PingTimeoutSec:       helper.MongoPingTimeoutSec,
	})
	if err != nil {
		log.Fatal(err)
	}

	testRPCServer, err = rpc.NewServer(&rpc.Config{
		Port: testRPCPort,
	}, be)
	if err != nil {
		log.Fatal(err)
	}

	if err := testRPCServer.Start(); err != nil {
		log.Fatalf("failed rpc listen: %s\n", err)
	}

	code := m.Run()

	if err := be.Close(); err != nil {
		log.Fatal(err)
	}
	testRPCServer.Shutdown(true)
	os.Exit(code)
}

func TestRPCServerBackend(t *testing.T) {
	t.Run("activate/deactivate client test", func(t *testing.T) {
		activateResp, err := testRPCServer.ActivateClient(
			context.Background(),
			&api.ActivateClientRequest{ClientKey: t.Name()},
		)
		assert.NoError(t, err)

		_, err = testRPCServer.DeactivateClient(
			context.Background(),
			&api.DeactivateClientRequest{ClientId: activateResp.ClientId},
		)
		assert.NoError(t, err)

		// invalid argument
		_, err = testRPCServer.ActivateClient(
			context.Background(),
			&api.ActivateClientRequest{ClientKey: ""},
		)
		assert.Equal(t, codes.InvalidArgument, status.Convert(err).Code())

		_, err = testRPCServer.DeactivateClient(
			context.Background(),
			&api.DeactivateClientRequest{ClientId: ""},
		)
		assert.Equal(t, codes.InvalidArgument, status.Convert(err).Code())

		// client not found
		_, err = testRPCServer.DeactivateClient(
			context.Background(),
			&api.DeactivateClientRequest{ClientId: nilClientID},
		)
		assert.Equal(t, codes.NotFound, status.Convert(err).Code())
	})

	t.Run("attach/detach document test", func(t *testing.T) {
		activateResp, err := testRPCServer.ActivateClient(
			context.Background(),
			&api.ActivateClientRequest{ClientKey: t.Name()},
		)
		assert.NoError(t, err)

		packWithNoChanges := &api.ChangePack{
			DocumentKey: &api.DocumentKey{
				Collection: t.Name(), Document: t.Name(),
			},
			Checkpoint: &api.Checkpoint{ServerSeq: 0, ClientSeq: 0},
		}

		_, err = testRPCServer.AttachDocument(
			context.Background(),
			&api.AttachDocumentRequest{
				ClientId:   activateResp.ClientId,
				ChangePack: packWithNoChanges,
			},
		)
		assert.NoError(t, err)

		// try to attach with invalid client ID
		_, err = testRPCServer.AttachDocument(
			context.Background(),
			&api.AttachDocumentRequest{
				ClientId:   "invalid",
				ChangePack: packWithNoChanges,
			},
		)
		assert.Equal(t, codes.InvalidArgument, status.Convert(err).Code())

		// try to attach with invalid client
		_, err = testRPCServer.AttachDocument(
			context.Background(),
			&api.AttachDocumentRequest{
				ClientId:   nilClientID,
				ChangePack: packWithNoChanges,
			},
		)
		assert.Equal(t, codes.NotFound, status.Convert(err).Code())

		// try to attach already attached document
		_, err = testRPCServer.AttachDocument(
			context.Background(),
			&api.AttachDocumentRequest{
				ClientId:   activateResp.ClientId,
				ChangePack: packWithNoChanges,
			},
		)
		assert.Equal(t, codes.FailedPrecondition, status.Convert(err).Code())

		// try to attach invalid change pack
		_, err = testRPCServer.AttachDocument(
			context.Background(),
			&api.AttachDocumentRequest{
				ClientId:   activateResp.ClientId,
				ChangePack: invalidChangePack,
			},
		)
		assert.Equal(t, codes.InvalidArgument, status.Convert(err).Code())

		_, err = testRPCServer.DetachDocument(
			context.Background(),
			&api.DetachDocumentRequest{
				ClientId:   activateResp.ClientId,
				ChangePack: packWithNoChanges,
			},
		)
		assert.NoError(t, err)

		// try to detach already detached document
		_, err = testRPCServer.DetachDocument(
			context.Background(),
			&api.DetachDocumentRequest{
				ClientId:   activateResp.ClientId,
				ChangePack: packWithNoChanges,
			},
		)
		assert.Equal(t, codes.FailedPrecondition, status.Convert(err).Code())

		_, err = testRPCServer.DetachDocument(
			context.Background(),
			&api.DetachDocumentRequest{
				ClientId:   activateResp.ClientId,
				ChangePack: invalidChangePack,
			},
		)
		assert.Equal(t, codes.InvalidArgument, status.Convert(err).Code())

		// document not found
		_, err = testRPCServer.DetachDocument(
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

		_, err = testRPCServer.DeactivateClient(
			context.Background(),
			&api.DeactivateClientRequest{ClientId: activateResp.ClientId},
		)
		assert.NoError(t, err)

		// try to attach the document with a deactivated client
		_, err = testRPCServer.AttachDocument(
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

		activateResp, err := testRPCServer.ActivateClient(
			context.Background(),
			&api.ActivateClientRequest{ClientKey: t.Name()},
		)
		assert.NoError(t, err)

		_, err = testRPCServer.AttachDocument(
			context.Background(),
			&api.AttachDocumentRequest{
				ClientId:   activateResp.ClientId,
				ChangePack: packWithNoChanges,
			},
		)
		assert.NoError(t, err)

		_, err = testRPCServer.PushPull(
			context.Background(),
			&api.PushPullRequest{
				ClientId:   activateResp.ClientId,
				ChangePack: packWithNoChanges,
			},
		)
		assert.NoError(t, err)

		_, err = testRPCServer.DetachDocument(
			context.Background(),
			&api.DetachDocumentRequest{
				ClientId:   activateResp.ClientId,
				ChangePack: packWithNoChanges,
			},
		)
		assert.NoError(t, err)

		// try to push/pull with detached document
		_, err = testRPCServer.PushPull(
			context.Background(),
			&api.PushPullRequest{
				ClientId:   activateResp.ClientId,
				ChangePack: packWithNoChanges,
			},
		)
		assert.Equal(t, codes.FailedPrecondition, status.Convert(err).Code())

		// try to push/pull with invalid pack
		_, err = testRPCServer.PushPull(
			context.Background(),
			&api.PushPullRequest{
				ClientId:   activateResp.ClientId,
				ChangePack: invalidChangePack,
			},
		)
		assert.Equal(t, codes.InvalidArgument, status.Convert(err).Code())

		_, err = testRPCServer.DeactivateClient(
			context.Background(),
			&api.DeactivateClientRequest{ClientId: activateResp.ClientId},
		)
		assert.NoError(t, err)

		// try to push/pull with deactivated client
		_, err = testRPCServer.PushPull(
			context.Background(),
			&api.PushPullRequest{
				ClientId:   activateResp.ClientId,
				ChangePack: packWithNoChanges,
			},
		)
		assert.Equal(t, codes.FailedPrecondition, status.Convert(err).Code())
	})
}
