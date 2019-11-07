package client

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"google.golang.org/grpc"

	"github.com/hackerwins/yorkie/api"
	"github.com/hackerwins/yorkie/api/converter"
	"github.com/hackerwins/yorkie/pkg/document"
	"github.com/hackerwins/yorkie/pkg/document/time"
	"github.com/hackerwins/yorkie/pkg/log"
)

type status int

const (
	deactivated status = 0
	activated   status = 1
)

var (
	ErrClientNotActivated  = errors.New("client is not activated")
	ErrDocumentNotAttached = errors.New("document is not attached")
)

type Client struct {
	conn   *grpc.ClientConn
	client api.YorkieClient

	id           *time.ActorID
	key          string
	status       status
	attachedDocs map[string]*document.Document
}

func NewClient(rpcAddr string, opts ...string) (*Client, error) {
	var k string
	if len(opts) == 0 {
		k = uuid.New().String()
	} else {
		k = opts[0]
	}

	conn, err := grpc.Dial(rpcAddr, grpc.WithInsecure())
	if err != nil {
		log.Logger.Error(err)
		return nil, err
	}

	client := api.NewYorkieClient(conn)

	return &Client{
		conn:         conn,
		client:       client,
		key:          k,
		status:       deactivated,
		attachedDocs: make(map[string]*document.Document),
	}, nil
}

func (c *Client) Close() error {
	if err := c.Deactivate(context.Background()); err != nil {
		return err
	}

	if err := c.conn.Close(); err != nil {
		log.Logger.Error(err)
		return err
	}

	return nil
}

func (c *Client) Activate(ctx context.Context) error {
	if c.status == activated {
		return nil
	}

	reply, err := c.client.ActivateClient(ctx, &api.ActivateClientRequest{
		ClientKey: c.key,
	})

	if err != nil {
		log.Logger.Error(err)
		return err
	}

	c.status = activated
	c.id = time.ActorIDFromHex(reply.ClientId)

	return nil
}

func (c *Client) Deactivate(ctx context.Context) error {
	if c.status == deactivated {
		return nil
	}

	_, err := c.client.DeactivateClient(ctx, &api.DeactivateClientRequest{
		ClientId: c.id.String(),
	})
	if err != nil {
		log.Logger.Error(err)
		return err
	}

	c.status = deactivated

	return nil
}

func (c *Client) AttachDocument(ctx context.Context, doc *document.Document) error {
	if c.status != activated {
		return ErrClientNotActivated
	}

	doc.SetActor(c.id)

	res, err := c.client.AttachDocument(ctx, &api.AttachDocumentRequest{
		ClientId:   c.id.String(),
		ChangePack: converter.ToChangePack(doc.FlushChangePack()),
	})
	if err != nil {
		log.Logger.Error(err)
		return err
	}

	pack := converter.FromChangePack(res.ChangePack)
	if err := doc.ApplyChangePack(pack); err != nil {
		log.Logger.Error(err)
		return err
	}

	c.attachedDocs[doc.Key().BSONKey()] = doc

	return nil
}

func (c *Client) DetachDocument(ctx context.Context, doc *document.Document) error {
	if c.status != activated {
		return ErrClientNotActivated
	}

	if _, ok := c.attachedDocs[doc.Key().BSONKey()]; !ok {
		return ErrDocumentNotAttached
	}

	res, err := c.client.DetachDocument(ctx, &api.DetachDocumentRequest{
		ClientId:   c.id.String(),
		ChangePack: converter.ToChangePack(doc.FlushChangePack()),
	})
	if err != nil {
		log.Logger.Error(err)
		return err
	}

	pack := converter.FromChangePack(res.ChangePack)
	if err := doc.ApplyChangePack(pack); err != nil {
		log.Logger.Error(err)
		return err
	}

	delete(c.attachedDocs, doc.Key().BSONKey())

	return nil
}

func (c *Client) PushPull(ctx context.Context) error {
	if c.status != activated {
		return ErrClientNotActivated
	}

	for _, doc := range c.attachedDocs {
		res, err := c.client.PushPull(ctx, &api.PushPullRequest{
			ClientId:   c.id.String(),
			ChangePack: converter.ToChangePack(doc.FlushChangePack()),
		})
		if err != nil {
			log.Logger.Error(err)
			return err
		}

		pack := converter.FromChangePack(res.ChangePack)
		if err := doc.ApplyChangePack(pack); err != nil {
			log.Logger.Error(err)
			return err
		}
	}

	return nil
}
