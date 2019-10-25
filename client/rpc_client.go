package client

import (
	"context"

	"google.golang.org/grpc"

	"github.com/hackerwins/rottie/api"
	"github.com/hackerwins/rottie/pkg/log"
)

const (
	rpcAddr = "localhost:1101"
)

type RPCClient struct {
	conn   *grpc.ClientConn
	client api.RottieClient
}

func NewRPCClient() (*RPCClient, error) {
	conn, err := grpc.Dial(rpcAddr, grpc.WithInsecure())
	if err != nil {
		log.Logger.Error(err)
		return nil, err
	}

	client := api.NewRottieClient(conn)

	return &RPCClient{
		conn:   conn,
		client: client,
	}, nil
}

func (c *RPCClient) Close() error {
	if err := c.conn.Close(); err != nil {
		log.Logger.Error(err)
		return err
	}
	return nil
}

func (c *RPCClient) Hello(body string) error {
	_, err := c.client.Hello(context.Background(), &api.HelloRequest{
		One: body,
	})
	if err != nil {
		return err
	}

	return nil
}
