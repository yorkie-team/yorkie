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

type Client struct {
	conn   *grpc.ClientConn
	client api.RottieClient
	key    string
}

func NewClient(key string) (*Client, error) {
	conn, err := grpc.Dial(rpcAddr, grpc.WithInsecure())
	if err != nil {
		log.Logger.Error(err)
		return nil, err
	}

	client := api.NewRottieClient(conn)

	return &Client{
		conn:   conn,
		client: client,
		key:    key,
	}, nil
}

func (c *Client) Close() error {
	if err := c.conn.Close(); err != nil {
		log.Logger.Error(err)
		return err
	}
	return nil
}

func (c *Client) Activate() error {
	_, err := c.client.Activate(context.Background(), &api.ActivateRequest{
		ClientKey: c.key,
	})
	if err != nil {
		log.Logger.Error(err)
		return err
	}

	return nil
}
