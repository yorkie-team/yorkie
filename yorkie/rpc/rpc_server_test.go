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
			// normal case
			activateResp, err := rpcServer.ActivateClient(context.Background(), &api.ActivateClientRequest{
				ClientKey: t.Name(),
			})
			assert.Nil(t, err)

			_, err = rpcServer.DeactivateClient(context.Background(), &api.DeactivateClientRequest{
				ClientId: activateResp.ClientId,
			})
			assert.Nil(t, err)

			// invalid argument
			_, err = rpcServer.ActivateClient(context.Background(), &api.ActivateClientRequest{
				ClientKey: "",
			})
			assert.Equal(t, status.Convert(err).Code(), codes.InvalidArgument)

			_, err = rpcServer.DeactivateClient(context.Background(), &api.DeactivateClientRequest{
				ClientId: "",
			})
			assert.Equal(t, status.Convert(err).Code(), codes.InvalidArgument)

			// client not found
			_, err = rpcServer.DeactivateClient(context.Background(), &api.DeactivateClientRequest{
				ClientId: "000000000000000000000000",
			})
			assert.Equal(t, status.Convert(err).Code(), codes.NotFound)
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
